package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

func TestServeGracefulShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(started) })
			<-release
			_, _ = io.WriteString(w, "done")
		}),
		ReadHeaderTimeout: time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- serve(ctx, server, listener, 3*time.Second)
	}()
	client := &http.Client{Timeout: 3 * time.Second}
	requestResult := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if err != nil {
			requestResult <- err
			return
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			requestResult <- err
			return
		}
		if string(data) != "done" {
			requestResult <- errors.New("unexpected response body")
			return
		}
		requestResult <- nil
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	close(release)
	select {
	case err := <-serverResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("shutdown timed out")
	}
	if err := <-requestResult; err != nil {
		t.Fatal(err)
	}
}

func TestServeListenerFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	if err := serve(context.Background(), server, listener, time.Second); err == nil {
		t.Fatal("expected listener error")
	}
}
