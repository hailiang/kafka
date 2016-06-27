package broker

import (
	"net"
	"sync"
	"time"

	"gopkg.in/fatih/pool.v2"
	"h12.me/kpax/model"
)

const defaultTimeout = 30 * time.Second

type PoolBroker struct {
	Timeout time.Duration
	MaxCap  int
	addr    string

	p  pool.Pool
	mu sync.Mutex
}

func NewPoolBroker(addr string) model.Broker {
	return &PoolBroker{
		addr:    addr,
		MaxCap:  10,
		Timeout: defaultTimeout,
	}
}

func (b *PoolBroker) Do(req model.Request, resp model.Response) error {
	var (
		err error
		p   pool.Pool
	)
	b.mu.Lock()
	if b.p == nil {
		b.p, err = pool.NewChannelPool(0, b.MaxCap, func() (net.Conn, error) {
			return net.DialTimeout("tcp", b.addr, b.Timeout)
		})
	}
	p = b.p
	b.mu.Unlock()
	if err != nil {
		return err
	}

	conn, err := p.Get()
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetWriteDeadline(time.Now().Add(b.Timeout)); err != nil {
		return err
	}
	if err := req.Send(conn); err != nil {
		return err
	}
	if resp == nil {
		return nil
	}
	if err := conn.SetReadDeadline(time.Now().Add(b.Timeout)); err != nil {
		return err
	}
	return resp.Receive(conn)
}

func (b *PoolBroker) Close() {
	b.mu.Lock()
	if b.p != nil {
		b.p.Close()
		b.p = nil
	}
	b.mu.Unlock()
}
