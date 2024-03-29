package clntside

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	// "go.etcd.io/etcd/api/v3"
	"go.etcd.io/etcd/api/v3/mvccpb"
	client "go.etcd.io/etcd/client/v3"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
)

const schema = "grpclb"

//ServiceDiscovery 服务发现
type ServiceDiscovery struct {
	cli        *client.Client //etcd client
	cc         resolver.ClientConn
	serverList sync.Map //服务列表
	prefix     string   //监视的前缀
}

//NewServiceDiscovery  新建发现服务
// endpoints: etcd node address
func NewServiceDiscovery(endpoints []string) *ServiceDiscovery {
	cli, err := client.New(client.Config{
		Endpoints:   endpoints,
		DialTimeout: 5 * time.Second,
	})
	if err != nil {
		log.Fatal(err)
	}
	r := &ServiceDiscovery{
		cli: cli,
	}
	resolver.Register(r)

	return r
}

func (s ServiceDiscovery) Dial(srvname string) (*grpc.ClientConn, error) {
	return grpc.Dial(
		fmt.Sprintf("%s:///%s", s.Scheme(), srvname),
		grpc.WithDefaultServiceConfig(fmt.Sprintf(`{"LoadBalancingPolicy": "%s"}`, "weight")),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
}

//Build 为给定目标创建一个新的`resolver`，当调用`grpc.Dial()`时执行
func (s *ServiceDiscovery) Build(target resolver.Target, cc resolver.ClientConn, opts resolver.BuildOptions) (resolver.Resolver, error) {
	s.cc = cc
	s.prefix = "/" + target.URL.Scheme + target.URL.Path + "/"
	//根据前缀获取现有的key
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	resp, err := s.cli.Get(ctx, s.prefix, client.WithPrefix())
	if err != nil {
		return nil, err
	}

	for _, ev := range resp.Kvs {
		s.SetServiceList(string(ev.Key), string(ev.Value))
	}
	s.cc.UpdateState(resolver.State{Addresses: s.getServices()})
	//监视前缀，修改变更的server
	go s.watcher()
	return s, nil
}

// ResolveNow 监视目标更新
func (s *ServiceDiscovery) ResolveNow(rn resolver.ResolveNowOptions) {
	log.Println("ResolveNow")
}

//Scheme return schema
func (s *ServiceDiscovery) Scheme() string {
	return schema
}

//Close 关闭
func (s *ServiceDiscovery) Close() {
	log.Println("Close")
	s.cli.Close()
}

//watcher 监听前缀
func (s *ServiceDiscovery) watcher() {
	rch := s.cli.Watch(context.Background(), s.prefix, client.WithPrefix())
	log.Printf("watching prefix:%s now...", s.prefix)
	for wresp := range rch {
		for _, ev := range wresp.Events {
			switch ev.Type {
			case mvccpb.PUT: //新增或修改
				s.SetServiceList(string(ev.Kv.Key), string(ev.Kv.Value))
			case mvccpb.DELETE: //删除
				s.DelServiceList(string(ev.Kv.Key))
			}
		}
	}
}

//SetServiceList 设置服务地址
func (s *ServiceDiscovery) SetServiceList(key, val string) {
	//获取服务地址
	addr := resolver.Address{Addr: strings.TrimPrefix(key, s.prefix)}
	//获取服务地址权重
	nodeWeight, err := strconv.Atoi(val)
	if err != nil {
		//非数字字符默认权重为1
		nodeWeight = 1
	}
	//把服务地址权重存储到resolver.Address的元数据中
	addr = SetAddrInfo(addr, AddrInfo{Weight: nodeWeight})
	s.serverList.Store(key, addr)
	s.cc.UpdateState(resolver.State{Addresses: s.getServices()})
}

//DelServiceList 删除服务地址
func (s *ServiceDiscovery) DelServiceList(key string) {
	s.serverList.Delete(key)
	s.cc.UpdateState(resolver.State{Addresses: s.getServices()})
}

//GetServices 获取服务地址
func (s *ServiceDiscovery) getServices() []resolver.Address {
	addrs := make([]resolver.Address, 0, 10)
	s.serverList.Range(func(k, v interface{}) bool {
		addrs = append(addrs, v.(resolver.Address))
		return true
	})
	return addrs
}
