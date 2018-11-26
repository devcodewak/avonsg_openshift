package helper

import (
	"bufio"
	"net"
)

type BufConn struct {
	net.Conn
	BR *bufio.Reader
}

func (c *BufConn) Peek(n int) ([]byte, error) {
	return c.BR.Peek(n)
}

func (c *BufConn) Read(b []byte) (n int, err error) {
	return c.BR.Read(b)
}

func (c *BufConn) Write(b []byte) (n int, err error) {
	return c.Conn.Write(b)
}

func (c *BufConn) Reset(conn net.Conn) {
	c.Conn = conn
}

func NewBufConn(c net.Conn, r *bufio.Reader) *BufConn {
	conn := &BufConn{Conn: c}
	conn.BR = r
	if nil == r {
		conn.BR = bufio.NewReader(c)
	}
	return conn
}
