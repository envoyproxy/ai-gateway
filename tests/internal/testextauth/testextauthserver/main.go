// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/envoyproxy/go-control-plane/envoy/service/auth/v3"
	"google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"github.com/envoyproxy/ai-gateway/tests/internal/testextauth"
)

var logger = log.New(os.Stdout, "[testextauthz] ", 0)

func main() {
	srv := doMain()
	defer srv.Stop()

	// Block until a terminate signal is received (SIGINT or SIGTERM).
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	s := <-sigCh
	logger.Printf("received signal %v, shutting down", s)
}

func doMain() *grpc.Server {
	portStr := os.Getenv("LISTENER_PORT")
	if portStr == "" {
		portStr = "1073"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		logger.Fatalf("invalid port: %v", err)
	}

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		logger.Fatalf("failed to listen: %v", err)
	}

	server := grpc.NewServer()
	authv3.RegisterAuthorizationServer(server, &ExtAuthServer{
		AllowedHeaderValue: os.Getenv(testextauth.ExtAuthAllowedValueEnvVar),
	})

	go func() {
		logger.Printf("starting ext auth server on port: %d", port)
		if err := server.Serve(lis); err != nil {
			logger.Fatalf("failed to serve: %v", err)
		}
	}()

	return server
}

type ExtAuthServer struct {
	AllowedHeaderValue string
}

func (e *ExtAuthServer) Check(_ context.Context, req *authv3.CheckRequest) (response *authv3.CheckResponse, err error) {
	headers := req.GetAttributes().GetRequest().GetHttp().GetHeaders()
	logger.Printf("checking request with headers: %v", headers)

	if e.AllowedHeaderValue == "" {
		logger.Println("no allow value configured. allowing request")
		return &authv3.CheckResponse{Status: &status.Status{Code: int32(codes.OK)}}, nil
	}

	if headers != nil && headers[testextauth.ExtAuthAccessControlHeader] == e.AllowedHeaderValue {
		logger.Printf("access control matches %q. allowed.", e.AllowedHeaderValue)
		return &authv3.CheckResponse{Status: &status.Status{Code: int32(codes.OK)}}, nil
	}

	logger.Printf("access control does not match %q. denied.", e.AllowedHeaderValue)
	return &authv3.CheckResponse{Status: &status.Status{Code: int32(codes.PermissionDenied), Message: "access denied"}}, nil
}
