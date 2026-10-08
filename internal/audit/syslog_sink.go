package audit

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// SyslogSinkOptions configures a syslog audit output.
type SyslogSinkOptions struct {
	// URL selects transport and address: udp://host:514, tcp://host:514,
	// tls://host:6514, unix:///dev/log or unixgram:///dev/log.
	URL string
	// Format is "rfc5424" (default) or "cef" (ArcSight Common Event Format
	// carried in an RFC 5424 message).
	Format string
	// Facility is a syslog facility name; default "local0".
	Facility string
	// AppName is the RFC 5424 APP-NAME; default "kervan".
	AppName string
	// Hostname overrides the HOSTNAME field; default os.Hostname().
	Hostname string
	// Version is reported as the CEF device version.
	Version string
	// TLSConfig is used for tls://; nil verifies against the system roots.
	TLSConfig *tls.Config
	// Timeout bounds dialing and each write; default 5s.
	Timeout time.Duration
}

var syslogFacilities = map[string]int{
	"kern": 0, "user": 1, "mail": 2, "daemon": 3, "auth": 4, "syslog": 5, "lpr": 6, "news": 7,
	"uucp": 8, "cron": 9, "authpriv": 10, "ftp": 11,
	"local0": 16, "local1": 17, "local2": 18, "local3": 19, "local4": 20, "local5": 21, "local6": 22, "local7": 23,
}

// SyslogSink sends audit events to a syslog collector. Stream transports
// use RFC 6587 octet-counting framing and reconnect after failures.
type SyslogSink struct {
	network  string
	address  string
	stream   bool
	format   string
	facility int
	appName  string
	hostname string
	version  string
	tlsCfg   *tls.Config
	timeout  time.Duration
	pid      string

	mu   sync.Mutex
	conn net.Conn
}

// ValidateSyslogURL reports whether raw is a supported syslog destination.
func ValidateSyslogURL(raw string) error {
	_, _, _, err := parseSyslogURL(raw)
	return err
}

func parseSyslogURL(raw string) (network, address string, stream bool, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false, fmt.Errorf("invalid syslog url: %w", err)
	}
	switch u.Scheme {
	case "udp", "tcp", "tls":
		if u.Host == "" {
			return "", "", false, errors.New("syslog url needs host:port")
		}
		address = u.Host
		if u.Port() == "" {
			port := "514"
			if u.Scheme == "tls" {
				port = "6514"
			}
			address = net.JoinHostPort(u.Hostname(), port)
		}
		return u.Scheme, address, u.Scheme != "udp", nil
	case "unix", "unixgram":
		if u.Path == "" {
			return "", "", false, errors.New("syslog unix url needs a socket path")
		}
		return u.Scheme, u.Path, u.Scheme == "unix", nil
	default:
		return "", "", false, errors.New("syslog url scheme must be udp, tcp, tls, unix or unixgram")
	}
}

func NewSyslogSink(opts SyslogSinkOptions) (*SyslogSink, error) {
	network, address, stream, err := parseSyslogURL(opts.URL)
	if err != nil {
		return nil, err
	}
	format := strings.ToLower(strings.TrimSpace(opts.Format))
	switch format {
	case "":
		format = "rfc5424"
	case "rfc5424", "cef":
	default:
		return nil, errors.New("syslog format must be rfc5424 or cef")
	}
	facilityName := strings.ToLower(strings.TrimSpace(opts.Facility))
	if facilityName == "" {
		facilityName = "local0"
	}
	facility, ok := syslogFacilities[facilityName]
	if !ok {
		return nil, fmt.Errorf("unknown syslog facility %q", opts.Facility)
	}
	hostname := opts.Hostname
	if hostname == "" {
		hostname, _ = os.Hostname()
	}
	appName := opts.AppName
	if appName == "" {
		appName = "kervan"
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	tlsCfg := opts.TLSConfig
	if network == "tls" {
		if tlsCfg == nil {
			tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		} else {
			tlsCfg = tlsCfg.Clone()
		}
		if tlsCfg.ServerName == "" {
			host, _, _ := net.SplitHostPort(address)
			tlsCfg.ServerName = host
		}
	}
	return &SyslogSink{
		network:  network,
		address:  address,
		stream:   stream,
		format:   format,
		facility: facility,
		appName:  headerToken(appName, 48),
		hostname: headerToken(hostname, 255),
		version:  opts.Version,
		tlsCfg:   tlsCfg,
		timeout:  timeout,
		pid:      strconv.Itoa(os.Getpid()),
	}, nil
}

func (s *SyslogSink) dial() (net.Conn, error) {
	dialer := &net.Dialer{Timeout: s.timeout}
	if s.network == "tls" {
		return tls.DialWithDialer(dialer, "tcp", s.address, s.tlsCfg)
	}
	return dialer.Dial(s.network, s.address)
}

// Write sends one event, reconnecting once if the connection has failed.
func (s *SyslogSink) Write(_ context.Context, evt Event) error {
	msg := s.Format(evt)
	frame := []byte(msg)
	if s.stream {
		frame = []byte(strconv.Itoa(len(msg)) + " " + msg)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if s.conn == nil {
			conn, err := s.dial()
			if err != nil {
				lastErr = err
				continue
			}
			s.conn = conn
		}
		_ = s.conn.SetWriteDeadline(time.Now().Add(s.timeout))
		if _, err := s.conn.Write(frame); err != nil {
			lastErr = err
			_ = s.conn.Close()
			s.conn = nil
			continue
		}
		return nil
	}
	return fmt.Errorf("syslog %s://%s: %w", s.network, s.address, lastErr)
}

func (s *SyslogSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// isWarning marks events that indicate a refused or failed action.
func isWarning(evt Event) bool {
	return evt.Type == EventAuthFailure || evt.Type == EventConnectionRejected ||
		strings.EqualFold(evt.Status, "failed") || strings.EqualFold(evt.Status, "rejected")
}

// Format renders evt as an RFC 5424 message (without transport framing).
func (s *SyslogSink) Format(evt Event) string {
	severity := 6 // informational
	if isWarning(evt) {
		severity = 4 // warning
	}
	ts := evt.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	msgID := headerToken(string(evt.Type), 32)
	header := fmt.Sprintf("<%d>1 %s %s %s %s %s",
		s.facility*8+severity,
		ts.UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
		s.hostname, s.appName, s.pid, msgID)

	if s.format == "cef" {
		return header + " - " + s.cef(evt, ts)
	}
	var sd strings.Builder
	// 32473 is the IANA private enterprise number reserved for
	// documentation (RFC 5612); collectors key on the SD-ID name.
	sd.WriteString("[kervan@32473")
	for _, kv := range [][2]string{
		{"id", evt.ID}, {"user", evt.Username}, {"protocol", evt.Protocol},
		{"path", evt.Path}, {"ip", evt.IP}, {"status", evt.Status},
	} {
		if kv[1] != "" {
			sd.WriteString(" " + kv[0] + "=\"" + sdEscape(kv[1]) + "\"")
		}
	}
	sd.WriteString("]")
	return header + " " + sd.String() + " " + oneLine(evt.Message)
}

// cefNames gives human-readable CEF event names for known types.
var cefNames = map[EventType]string{
	EventAuthSuccess:        "Authentication succeeded",
	EventAuthFailure:        "Authentication failed",
	EventFileRead:           "File downloaded",
	EventFileWrite:          "File uploaded",
	EventFileDelete:         "File deleted",
	EventConnectionRejected: "Connection rejected",
}

func (s *SyslogSink) cef(evt Event, ts time.Time) string {
	name := cefNames[evt.Type]
	if name == "" {
		name = string(evt.Type)
	}
	severity := "3"
	if isWarning(evt) {
		severity = "6"
	}
	version := s.version
	if version == "" {
		version = "dev"
	}
	var ext []string
	add := func(k, v string) {
		if v != "" {
			ext = append(ext, k+"="+cefExtEscape(v))
		}
	}
	add("rt", strconv.FormatInt(ts.UnixMilli(), 10))
	add("externalId", evt.ID)
	add("suser", evt.Username)
	if host, port, err := net.SplitHostPort(evt.IP); err == nil {
		add("src", host)
		add("spt", port)
	} else {
		add("src", evt.IP)
	}
	add("app", evt.Protocol)
	add("filePath", evt.Path)
	add("outcome", evt.Status)
	add("msg", evt.Message)
	return strings.Join([]string{
		"CEF:0", "Kervan", "Kervan", cefHeaderEscape(version), cefHeaderEscape(string(evt.Type)),
		cefHeaderEscape(name), severity, strings.Join(ext, " "),
	}, "|")
}

// headerToken makes an RFC 5424 header field: printable US-ASCII without
// spaces, or "-" when empty.
func headerToken(v string, maxLen int) string {
	var b strings.Builder
	for _, r := range v {
		if r > 32 && r < 127 {
			b.WriteRune(r)
		}
		if b.Len() >= maxLen {
			break
		}
	}
	if b.Len() == 0 {
		return "-"
	}
	return b.String()
}

func sdEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, `]`, `\]`).Replace(oneLine(v))
}

func cefHeaderEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `|`, `\|`).Replace(oneLine(v))
}

func cefExtEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, `=`, `\=`, "\n", `\n`, "\r", `\r`).Replace(v)
}

// oneLine replaces control characters so a value cannot forge extra
// records on line-oriented collectors.
func oneLine(v string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, v)
}
