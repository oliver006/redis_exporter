package exporter

import "errors"

type stubRedisConn struct {
	do       func(string, ...any) (any, error)
	closed   bool
	closeErr error
}

func (c *stubRedisConn) Do(commandName string, args ...any) (any, error) {
	return c.do(commandName, args...)
}

func (*stubRedisConn) Send(string, ...any) error { return errors.New("not implemented") }
func (*stubRedisConn) Flush() error              { return errors.New("not implemented") }
func (*stubRedisConn) Receive() (any, error)     { return nil, errors.New("not implemented") }
func (*stubRedisConn) Err() error                { return nil }
func (c *stubRedisConn) Close() error            { c.closed = true; return c.closeErr }

type clusterScanStub struct {
	*stubRedisConn
}

func (*clusterScanStub) scanCommand() string { return "CLUSTERSCAN" }
