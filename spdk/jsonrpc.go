// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2022-2023 Dell Inc, or its subsidiaries.
// Copyright (C) 2023 Intel Corporation

// Package spdk implements the spdk json-rpc protocol
package spdk

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync/atomic"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrFailedSpdkCall indicates that the bridge failed to execute SPDK call
	ErrFailedSpdkCall = status.Error(codes.Unknown, "Failed to execute SPDK call")
	// ErrUnexpectedSpdkCallResult indicates that the bridge got an error from SPDK
	ErrUnexpectedSpdkCallResult = status.Error(codes.FailedPrecondition, "Unexpected SPDK call result.")
)

// JSONRPC represents an interface to execute JSON RPC to SPDK
type JSONRPC interface {
	Call(method string, args, result interface{}) error
}

type spdkJSONRPC struct {
	transport string
	socket    string
	id        uint64
}

// NewSpdkJSONRPC creates a new instance of JSONRPC which is capable to
// interact with either unix domain socket, e.g.: /var/tmp/spdk.sock
// or with tcp connection ip and port tuple, e.g.: 10.1.1.2:1234
func NewSpdkJSONRPC(socketPath string) JSONRPC {
	if socketPath == "" {
		log.Panic("empty socketPath is not allowed")
	}
	protocol := "tcp"
	if _, _, err := net.SplitHostPort(socketPath); err != nil {
		protocol = "unix"
	}
	log.Printf("Connection to SPDK will be via: %s detected from %s", protocol, socketPath)
	return &spdkJSONRPC{
		transport: protocol,
		socket:    socketPath,
		id:        0,
	}
}

// Call implements low level rpc request/response handling
func (r *spdkJSONRPC) Call(method string, args, result interface{}) error {
	id := atomic.AddUint64(&r.id, 1)
	request := RPCRequest{
		RPCVersion: JSONRPCVersion,
		ID:         id,
		Method:     method,
		Params:     args,
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("%s: %s", method, err)
	}

	log.Printf("Sending to SPDK: %s", data)

	resp, _ := r.communicate(data)

	var response RPCResponse
	err = json.NewDecoder(resp).Decode(&response)
	jsonresponse, _ := json.Marshal(response)
	log.Printf("Received from SPDK: %s", jsonresponse)
	if err != nil {
		return fmt.Errorf("%s: %s", method, err)
	}
	if response.ID != id {
		return fmt.Errorf("%s: json response ID mismatch", method)
	}
	if response.Error.Code != 0 {
		return fmt.Errorf("%s: json response error: %s", method, response.Error.Message)
	}
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return fmt.Errorf("%s: %s", method, err)
	}
	return nil
}

func (r *spdkJSONRPC) communicate(buf []byte) (io.Reader, error) {
	// connect
	conn, err := net.Dial(r.transport, r.socket)
	if err != nil {
		log.Fatal(err)
	}
	// write
	_, err = conn.Write(buf)
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	// close
	switch conn := conn.(type) {
	case *net.TCPConn:
		err = conn.CloseWrite()
	case *net.UnixConn:
		err = conn.CloseWrite()
	}
	if err != nil {
		log.Fatal(err)
		return nil, err
	}
	// read
	return bufio.NewReader(conn), nil
}