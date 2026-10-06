package onboardinghost

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestMaintenanceStopCancelsAndWaitsForActiveWork(t *testing.T) {
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	stop := startMaintenance(t.Context(), time.Millisecond, func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		stop()
		t.Fatal("maintenance did not start")
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("work context was not cancelled")
	}
	select {
	case <-done:
		close(release)
		t.Fatal("stop returned before work released dependencies")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance did not stop")
	}
	stop() // A repeated cleanup must also be safe.
}

func TestShutdownWaitsForInFlightRequest(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, "completed")
	})}
	defer server.Close()
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, server, listener) }()
	response := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		r, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			defer r.Body.Close()
			var raw []byte
			raw, err = io.ReadAll(r.Body)
			if err == nil && string(raw) != "completed" {
				err = io.ErrUnexpectedEOF
			}
		}
		response <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		close(release)
		t.Fatalf("server returned before request drained: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	if err := <-response; err != nil {
		t.Fatal(err)
	}
}
