package audit

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

var testEvent = Event{
	ID:        "01EVT",
	Timestamp: time.Date(2026, 10, 8, 12, 0, 0, 123456000, time.UTC),
	Type:      EventAuthFailure,
	Username:  "ann",
	Protocol:  "sftp",
	IP:        "198.51.100.7:51022",
	Status:    "failed",
	Message:   "invalid credentials",
}

func TestSyslogRFC5424OverUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	sink, err := NewSyslogSink(SyslogSinkOptions{URL: "udp://" + pc.LocalAddr().String(), Facility: "auth", Hostname: "files01"})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	if err := sink.Write(context.Background(), testEvent); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4096)
	_ = pc.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	msg := string(buf[:n])
	// auth(4)*8 + warning(4) = 36
	wantPrefix := "<36>1 2026-10-08T12:00:00.123456Z files01 kervan "
	if !strings.HasPrefix(msg, wantPrefix) {
		t.Fatalf("header: %q", msg)
	}
	for _, want := range []string{" auth.failure [kervan@32473 id=\"01EVT\" user=\"ann\" protocol=\"sftp\" ip=\"198.51.100.7:51022\" status=\"failed\"] invalid credentials"} {
		if !strings.HasSuffix(msg, want) {
			t.Fatalf("message %q missing %q", msg, want)
		}
	}
}

func TestSyslogTCPFramingAndReconnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan string, 10)
	// serve accepts one connection, reads one octet-counted message and
	// then drops the connection, like a collector restarting.
	serve := func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		lenStr, err := r.ReadString(' ')
		if err != nil {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSpace(lenStr))
		msg := make([]byte, n)
		if _, err := io.ReadFull(r, msg); err == nil {
			received <- string(msg)
		}
	}
	go serve()
	sink, err := NewSyslogSink(SyslogSinkOptions{URL: "tcp://" + ln.Addr().String(), Hostname: "h"})
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	evt := testEvent
	evt.Message = "line one\nforged <13>1 record"
	if err := sink.Write(context.Background(), evt); err != nil {
		t.Fatal(err)
	}
	first := <-received
	if strings.Contains(first, "\n") {
		t.Fatalf("newline survived into the record: %q", first)
	}

	// The server dropped the connection; the next write reconnects.
	go serve()
	time.Sleep(50 * time.Millisecond)
	// The first write after the drop may vanish into the dead socket (TCP
	// reports the reset only on a later write); delivery must resume.
	delivered := false
	for i := 0; i < 3 && !delivered; i++ {
		_ = sink.Write(context.Background(), testEvent)
		select {
		case <-received:
			delivered = true
		case <-time.After(time.Second):
		}
	}
	if !delivered {
		t.Fatal("sink did not reconnect after the collector dropped the connection")
	}
}

func TestSyslogCEFFormat(t *testing.T) {
	sink, err := NewSyslogSink(SyslogSinkOptions{URL: "udp://127.0.0.1:9", Format: "cef", Hostname: "h", Version: "0.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	evt := testEvent
	evt.Username = `ev=il|user\`
	evt.Path = "/a=b"
	msg := sink.Format(evt)
	cef := msg[strings.Index(msg, "CEF:0"):]
	wantHead := "CEF:0|Kervan|Kervan|0.3.0|auth.failure|Authentication failed|6|"
	if !strings.HasPrefix(cef, wantHead) {
		t.Fatalf("CEF header: %q", cef)
	}
	for _, want := range []string{
		"rt=" + strconv.FormatInt(testEvent.Timestamp.UnixMilli(), 10),
		`suser=ev\=il|user\\`,
		"src=198.51.100.7 spt=51022",
		"app=sftp",
		`filePath=/a\=b`,
		"outcome=failed",
		"msg=invalid credentials",
	} {
		if !strings.Contains(cef, want) {
			t.Errorf("CEF %q missing %q", cef, want)
		}
	}
}

func TestSyslogStructuredDataEscaping(t *testing.T) {
	sink, _ := NewSyslogSink(SyslogSinkOptions{URL: "udp://127.0.0.1:9", Hostname: "h"})
	evt := Event{Type: EventFileWrite, Username: `x"] [forged@1 a="b`, Path: `/p\q`}
	msg := sink.Format(evt)
	if !strings.Contains(msg, `user="x\"\] [forged@1 a=\"b"`) || !strings.Contains(msg, `path="/p\\q"`) {
		t.Fatalf("structured data not escaped: %q", msg)
	}
	if !strings.HasPrefix(msg, "<134>") { // local0(16)*8 + info(6)
		t.Fatalf("priority: %q", msg)
	}
}

func TestSyslogOptionsValidation(t *testing.T) {
	for _, bad := range []SyslogSinkOptions{
		{URL: "http://x:514"},
		{URL: "tcp://"},
		{URL: "udp://h:514", Format: "json"},
		{URL: "udp://h:514", Facility: "nope"},
	} {
		if _, err := NewSyslogSink(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	s, err := NewSyslogSink(SyslogSinkOptions{URL: "tls://siem.example"})
	if err != nil || s.address != "siem.example:6514" || s.tlsCfg.ServerName != "siem.example" {
		t.Fatalf("tls defaults: %+v %v", s, err)
	}
}

// stuckCollector accepts TCP connections and never reads from them, like a
// syslog collector that has hung: once the socket buffers fill, every
// write blocks. release drops the connections, which fails any write still
// blocked on them; it also runs at cleanup and is safe to call twice.
func stuckCollector(t *testing.T) (addr string, release func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	release = func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	}
	t.Cleanup(release)
	return ln.Addr().String(), release
}

// fillUntilBlocked writes large events until one is still blocked after
// a short while, i.e. the collector's buffers are full. It returns that
// write's result channel.
func fillUntilBlocked(t *testing.T, sink *SyslogSink, ctx context.Context) <-chan error {
	t.Helper()
	evt := testEvent
	evt.Message = strings.Repeat("x", 64<<10)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		done := make(chan error, 1)
		go func() { done <- sink.Write(ctx, evt) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("write failed before the collector filled up: %v", err)
			}
		case <-time.After(300 * time.Millisecond):
			return done // this write is stuck
		}
	}
	t.Fatal("collector never stopped accepting data")
	return nil
}

// Cancelling the context interrupts a write that is stuck on a collector
// that stopped reading, instead of waiting out the sink's own timeout, and
// a cancelled context starts no new write.
func TestSyslogWriteHonoursContextCancellation(t *testing.T) {
	addr, release := stuckCollector(t)
	// A timeout far longer than the test: only cancellation can unblock.
	sink, err := NewSyslogSink(SyslogSinkOptions{URL: "tcp://" + addr, Hostname: "h", Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	// Drop the collector first: if cancellation did not work, a write still
	// holds the sink's lock and Close would otherwise wait for it.
	defer func() { release(); _ = sink.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	stuck := fillUntilBlocked(t, sink, ctx)
	start := time.Now()
	cancel()
	select {
	case err := <-stuck:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted write returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling the context did not interrupt a blocked syslog write")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("write took %v to notice cancellation", elapsed)
	}

	// The interrupted connection was discarded, and a cancelled context
	// does not even try again.
	sink.mu.Lock()
	connGone := sink.conn == nil
	sink.mu.Unlock()
	if !connGone {
		t.Fatal("connection with a partially written frame was kept")
	}
	if err := sink.Write(ctx, testEvent); !errors.Is(err, context.Canceled) {
		t.Fatalf("write with a cancelled context = %v, want context.Canceled", err)
	}
}

// End to end: Engine.Close must finish within its drain timeout when the
// syslog collector has stopped reading, and count what it gave up on.
func TestEngineCloseBoundedBySyslogCollectorThatStoppedReading(t *testing.T) {
	addr, release := stuckCollector(t)
	syslog, err := NewSyslogSink(SyslogSinkOptions{URL: "tcp://" + addr, Hostname: "h", Timeout: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	file := &recordingSink{}
	e := NewEngine(nil, file, syslog)
	e.drainTimeout = 500 * time.Millisecond

	// Enough large events to fill the socket buffers and leave more queued.
	big := strings.Repeat("y", 64<<10)
	const n = 400
	for i := 0; i < n; i++ {
		e.Emit(Event{Type: EventFileWrite, Path: "/f" + strconv.Itoa(i), Message: big})
	}

	start := time.Now()
	closed := make(chan struct{})
	go func() { e.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		release() // let the hung Close finish before the test ends
		<-closed
		t.Fatal("Engine.Close hung on a syslog collector that stopped reading")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("Engine.Close took %v with a 500ms drain timeout", elapsed)
	}
	if got := file.count(); got != n {
		t.Fatalf("file sink got %d of %d events", got, n)
	}
	st := e.Stats()[1]
	if st.Type != "syslog" || st.Dropped == 0 {
		t.Fatalf("syslog stats after a cut-off drain = %+v, want undelivered events counted as dropped", st)
	}
}
