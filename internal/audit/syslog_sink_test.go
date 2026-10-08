package audit

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
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
