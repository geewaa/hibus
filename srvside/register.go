package srvside

import (
	"context"
	"log"
	"strconv"
	"time"

	client "go.etcd.io/etcd/client/v3"
)

const schema = "grpclb"

//ServiceRegister 创建租约注册服务
type ServiceRegister struct {
	cli     *client.Client //etcd client
	leaseID client.LeaseID //租约ID
	//租约keepalieve相应chan
	keepAliveChan <-chan *client.LeaseKeepAliveResponse
	key           string //key
	weight        string //value
}

//NewServiceRegister 新建注册服务
func NewServiceRegister(endpoints []string, svrname, listenaddr string, weight int, lease int64) (*ServiceRegister, error) {
	cli, err := client.New(client.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}

	ser := &ServiceRegister{
		cli:    cli,
		key:    "/" + schema + "/" + svrname + "/" + listenaddr,
		weight: strconv.Itoa(weight),
	}

	//申请租约设置时间keepalive
	if err := ser.putKeyWithLease(lease); err != nil {
		return nil, err
	}

	return ser, nil
}

//设置租约
func (s *ServiceRegister) putKeyWithLease(lease int64) error {
	//设置租约时间
	resp, err := s.cli.Grant(context.Background(), lease)
	if err != nil {
		return err
	}
	//注册服务并绑定租约
	_, err = s.cli.Put(context.Background(), s.key, s.weight, client.WithLease(resp.ID))
	if err != nil {
		return err
	}
	//设置续租 定期发送需求请求
	leaseRespChan, err := s.cli.KeepAlive(context.Background(), resp.ID)

	if err != nil {
		return err
	}
	s.leaseID = resp.ID
	s.keepAliveChan = leaseRespChan
	go s.ListenLeaseRespChan()

	log.Printf("Put key:%s  weight:%s  success!", s.key, s.weight)
	return nil
}

//ListenLeaseRespChan 监听 续租情况
func (s *ServiceRegister) ListenLeaseRespChan() {
	for leaseKeepResp := range s.keepAliveChan {
		log.Println("续租成功", leaseKeepResp)
	}
	log.Println("关闭续租")
}

// Close 注销服务
func (s *ServiceRegister) Close() error {
	//撤销租约
	if _, err := s.cli.Revoke(context.Background(), s.leaseID); err != nil {
		return err
	}
	log.Println("撤销租约")
	return s.cli.Close()
}
