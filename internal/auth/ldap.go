package auth

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kervanserver/kervan/internal/config"
)

type LDAPIdentity struct {
	DN          string
	Username    string
	Email       string
	Groups      []string
	Type        UserType
	HomeDir     string
	Permissions UserPermissions
}

type LDAPProvider struct {
	cfg         config.LDAPConfig
	now         func() time.Time
	dialContext func(context.Context, string, string) (net.Conn, error)
	warn        func(string, ...any)
	warnOnce    sync.Once

	mu    sync.Mutex
	cache map[string]cachedLDAPIdentity
}

type cachedLDAPIdentity struct {
	identity  LDAPIdentity
	expiresAt time.Time
}

type ldapMessage struct {
	MessageID  int
	ProtocolOp berValue
}

type berValue struct {
	tag   byte
	value []byte
}

func NewLDAPProvider(cfg config.LDAPConfig) *LDAPProvider {
	if strings.TrimSpace(cfg.UsernameAttribute) == "" {
		cfg.UsernameAttribute = "uid"
	}
	if strings.TrimSpace(cfg.EmailAttribute) == "" {
		cfg.EmailAttribute = "mail"
	}
	if strings.TrimSpace(cfg.GroupAttribute) == "" {
		cfg.GroupAttribute = "memberOf"
	}
	if strings.TrimSpace(cfg.UserFilter) == "" {
		cfg.UserFilter = fmt.Sprintf("(%s=%%s)", cfg.UsernameAttribute)
	}
	if strings.TrimSpace(cfg.DefaultHomeDir) == "" {
		cfg.DefaultHomeDir = "/"
	}
	if cfg.CacheTTL < 0 {
		cfg.CacheTTL = 0
	}

	return &LDAPProvider{
		cfg:   cfg,
		now:   func() time.Time { return time.Now().UTC() },
		cache: make(map[string]cachedLDAPIdentity),
		warn:  func(string, ...any) {},
		dialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, address)
		},
	}
}

func (p *LDAPProvider) SetWarningLogger(warn func(string, ...any)) {
	if warn == nil {
		p.warn = func(string, ...any) {}
		return
	}
	p.warn = warn
}

func (p *LDAPProvider) Authenticate(ctx context.Context, username, password string) (*LDAPIdentity, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}

	identity, err := p.lookupIdentity(ctx, username)
	if err != nil {
		return nil, err
	}
	if err := p.bindUser(ctx, identity.DN, password); err != nil {
		return nil, err
	}
	p.storeCache(identity)
	return identity, nil
}

func (p *LDAPProvider) lookupIdentity(ctx context.Context, username string) (*LDAPIdentity, error) {
	if cached := p.cachedIdentity(username); cached != nil {
		return cached, nil
	}

	conn, err := p.dial(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if strings.TrimSpace(p.cfg.BindDN) != "" || p.cfg.BindPassword != "" {
		if err := ldapBind(conn, 1, p.cfg.BindDN, p.cfg.BindPassword); err != nil {
			return nil, err
		}
	}

	filter, err := parseLDAPFilter(p.renderedFilter(username))
	if err != nil {
		return nil, err
	}
	attrs := uniqueStrings([]string{
		p.cfg.UsernameAttribute,
		p.cfg.EmailAttribute,
		p.cfg.GroupAttribute,
	})
	if err := writeLDAPMessage(conn, searchRequestMessage(2, p.cfg.BaseDN, filter, attrs)); err != nil {
		return nil, err
	}

	reader := bufio.NewReader(conn)
	var identity *LDAPIdentity
	for {
		msg, err := readLDAPMessage(reader)
		if err != nil {
			return nil, err
		}
		switch msg.ProtocolOp.tag {
		case 0x64:
			entry, entryErr := parseSearchEntry(msg.ProtocolOp, p.cfg, username)
			if entryErr != nil {
				return nil, entryErr
			}
			identity = entry
		case 0x65:
			if err := parseLDAPResult(msg.ProtocolOp); err != nil {
				return nil, err
			}
			if identity == nil {
				return nil, ErrInvalidCredentials
			}
			return identity, nil
		}
	}
}

func (p *LDAPProvider) bindUser(ctx context.Context, dn, password string) error {
	conn, err := p.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return ldapBind(conn, 1, dn, password)
}

func (p *LDAPProvider) dial(ctx context.Context) (net.Conn, error) {
	rawURL, err := url.Parse(strings.TrimSpace(p.cfg.URL))
	if err != nil {
		return nil, err
	}
	address := rawURL.Host
	switch rawURL.Scheme {
	case "ldaps":
		if !strings.Contains(address, ":") {
			address += ":636"
		}
		if p.cfg.TLSSkipVerify {
			p.warnOnce.Do(func() {
				p.warn("ldap TLS certificate verification is disabled", "url", strings.TrimSpace(p.cfg.URL))
			})
		}
		dialer := &tls.Dialer{
			NetDialer: &net.Dialer{},
			Config: &tls.Config{
				ServerName: hostOnly(address),
				// #nosec G402 -- configurable for legacy/self-signed LDAP deployments.
				InsecureSkipVerify: p.cfg.TLSSkipVerify,
			},
		}
		return dialer.DialContext(ctx, "tcp", address)
	case "ldap":
		if !strings.Contains(address, ":") {
			address += ":389"
		}
		return p.dialContext(ctx, "tcp", address)
	default:
		return nil, fmt.Errorf("unsupported ldap scheme: %s", rawURL.Scheme)
	}
}

func (p *LDAPProvider) cachedIdentity(username string) *LDAPIdentity {
	if p.cfg.CacheTTL <= 0 {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	cached, ok := p.cache[strings.ToLower(username)]
	if !ok || p.now().After(cached.expiresAt) {
		delete(p.cache, strings.ToLower(username))
		return nil
	}
	dup := cached.identity
	return &dup
}

func (p *LDAPProvider) storeCache(identity *LDAPIdentity) {
	if identity == nil || p.cfg.CacheTTL <= 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[strings.ToLower(identity.Username)] = cachedLDAPIdentity{
		identity:  *identity,
		expiresAt: p.now().Add(p.cfg.CacheTTL),
	}
}

func (p *LDAPProvider) renderedFilter(username string) string {
	filter := strings.TrimSpace(p.cfg.UserFilter)
	escaped := escapeLDAPFilterValue(username)
	switch {
	case strings.Contains(filter, "%s"):
		return strings.ReplaceAll(filter, "%s", escaped)
	case strings.Contains(filter, "{username}"):
		return strings.ReplaceAll(filter, "{username}", escaped)
	default:
		return fmt.Sprintf("(%s=%s)", p.cfg.UsernameAttribute, escaped)
	}
}

func ldapBind(conn net.Conn, messageID int, dn, password string) error {
	if err := writeLDAPMessage(conn, bindRequestMessage(messageID, dn, password)); err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	msg, err := readLDAPMessage(reader)
	if err != nil {
		return err
	}
	if msg.ProtocolOp.tag != 0x61 {
		return errors.New("unexpected ldap bind response")
	}
	return parseLDAPResult(msg.ProtocolOp)
}

func bindRequestMessage(messageID int, dn, password string) []byte {
	body := append(encodeInteger(3), encodeOctetString(dn)...)
	body = append(body, encodeTLV(0x80, []byte(password))...)
	return encodeLDAPMessage(messageID, encodeTLV(0x60, body))
}

func searchRequestMessage(messageID int, baseDN string, filter []byte, attrs []string) []byte {
	body := append(encodeOctetString(baseDN), encodeEnumerated(2)...)
	body = append(body, encodeEnumerated(0)...)
	body = append(body, encodeInteger(1)...)
	body = append(body, encodeInteger(5)...)
	body = append(body, encodeBoolean(false)...)
	body = append(body, filter...)
	attrBody := make([]byte, 0)
	for _, attr := range attrs {
		attrBody = append(attrBody, encodeOctetString(attr)...)
	}
	body = append(body, encodeTLV(0x30, attrBody)...)
	return encodeLDAPMessage(messageID, encodeTLV(0x63, body))
}

func encodeLDAPMessage(messageID int, protocolOp []byte) []byte {
	body := append(encodeInteger(messageID), protocolOp...)
	return encodeTLV(0x30, body)
}

func writeLDAPMessage(w io.Writer, payload []byte) error {
	_, err := w.Write(payload)
	return err
}

func readLDAPMessage(r *bufio.Reader) (*ldapMessage, error) {
	top, err := readBERValue(r)
	if err != nil {
		return nil, err
	}
	if top.tag != 0x30 {
		return nil, errors.New("invalid ldap message")
	}
	children, err := parseBERChildren(top.value)
	if err != nil {
		return nil, err
	}
	if len(children) < 2 {
		return nil, errors.New("ldap message missing fields")
	}
	messageID, err := parseBERInt(children[0])
	if err != nil {
		return nil, err
	}
	return &ldapMessage{
		MessageID:  messageID,
		ProtocolOp: children[1],
	}, nil
}

func parseLDAPResult(op berValue) error {
	children, err := parseBERChildren(op.value)
	if err != nil {
		return err
	}
	if len(children) < 3 {
		return errors.New("ldap result missing fields")
	}
	resultCode, err := parseBERInt(children[0])
	if err != nil {
		return err
	}
	if resultCode != 0 {
		diagnostic := string(children[2].value)
		if strings.TrimSpace(diagnostic) == "" {
			diagnostic = "ldap operation failed"
		}
		if resultCode == 49 {
			return ErrInvalidCredentials
		}
		return errors.New(diagnostic)
	}
	return nil
}

func parseSearchEntry(op berValue, cfg config.LDAPConfig, fallbackUsername string) (*LDAPIdentity, error) {
	children, err := parseBERChildren(op.value)
	if err != nil {
		return nil, err
	}
	if len(children) < 2 {
		return nil, errors.New("ldap search entry missing fields")
	}
	entry := &LDAPIdentity{
		DN:          string(children[0].value),
		Username:    fallbackUsername,
		Type:        mapLDAPUserType(cfg.GroupMapping, nil),
		HomeDir:     renderLDAPHomeDir(cfg.DefaultHomeDir, fallbackUsername),
		Permissions: DefaultUserPermissions(),
	}

	attrs, err := parseBERChildren(children[1].value)
	if err != nil {
		return nil, err
	}
	for _, attr := range attrs {
		attrChildren, childErr := parseBERChildren(attr.value)
		if childErr != nil || len(attrChildren) < 2 {
			continue
		}
		name := strings.TrimSpace(string(attrChildren[0].value))
		values, valuesErr := parseBERChildren(attrChildren[1].value)
		if valuesErr != nil {
			continue
		}
		stringValues := make([]string, 0, len(values))
		for _, value := range values {
			stringValues = append(stringValues, string(value.value))
		}
		switch {
		case strings.EqualFold(name, cfg.UsernameAttribute):
			if len(stringValues) > 0 && strings.TrimSpace(stringValues[0]) != "" {
				entry.Username = strings.TrimSpace(stringValues[0])
				entry.HomeDir = renderLDAPHomeDir(cfg.DefaultHomeDir, entry.Username)
			}
		case strings.EqualFold(name, cfg.EmailAttribute):
			if len(stringValues) > 0 {
				entry.Email = strings.TrimSpace(stringValues[0])
			}
		case strings.EqualFold(name, cfg.GroupAttribute):
			entry.Groups = append(entry.Groups, stringValues...)
		}
	}
	entry.Type = mapLDAPUserType(cfg.GroupMapping, entry.Groups)
	return entry, nil
}

func parseBERChildren(data []byte) ([]berValue, error) {
	values := make([]berValue, 0)
	for len(data) > 0 {
		if len(data) < 2 {
			return nil, errors.New("invalid ber payload")
		}
		tag := data[0]
		length, headerLen, err := decodeBERLength(data[1:])
		if err != nil {
			return nil, err
		}
		start := 1 + headerLen
		end := start + length
		if end > len(data) {
			return nil, errors.New("invalid ber length")
		}
		values = append(values, berValue{
			tag:   tag,
			value: append([]byte(nil), data[start:end]...),
		})
		data = data[end:]
	}
	return values, nil
}

func readBERValue(r *bufio.Reader) (berValue, error) {
	tag, err := r.ReadByte()
	if err != nil {
		return berValue{}, err
	}
	length, err := readBERLength(r)
	if err != nil {
		return berValue{}, err
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(r, value); err != nil {
		return berValue{}, err
	}
	return berValue{tag: tag, value: value}, nil
}

func readBERLength(r *bufio.Reader) (int, error) {
	first, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	if first&0x80 == 0 {
		return int(first), nil
	}
	count := int(first & 0x7F)
	if count == 0 || count > 4 {
		return 0, errors.New("unsupported ber length")
	}
	length := 0
	for i := 0; i < count; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		length = (length << 8) | int(b)
	}
	return length, nil
}

func decodeBERLength(data []byte) (int, int, error) {
	if len(data) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	first := data[0]
	if first&0x80 == 0 {
		return int(first), 1, nil
	}
	count := int(first & 0x7F)
	if count == 0 || count > 4 || len(data) < 1+count {
		return 0, 0, errors.New("unsupported ber length")
	}
	length := 0
	for _, b := range data[1 : 1+count] {
		length = (length << 8) | int(b)
	}
	return length, 1 + count, nil
}

func parseBERInt(value berValue) (int, error) {
	if len(value.value) == 0 {
		return 0, errors.New("empty ber integer")
	}
	out := 0
	for _, b := range value.value {
		out = (out << 8) | int(b)
	}
	return out, nil
}

func encodeTLV(tag byte, value []byte) []byte {
	out := []byte{tag}
	out = append(out, encodeBERLength(len(value))...)
	out = append(out, value...)
	return out
}

func encodeBERLength(length int) []byte {
	if length < 0 {
		return []byte{0}
	}
	if length < 0x80 {
		// #nosec G115 -- guarded by length < 0x80.
		return []byte{byte(length)}
	}
	buf := make([]byte, 0, 4)
	for length > 0 {
		buf = append([]byte{byte(length & 0xFF)}, buf...)
		length >>= 8
	}
	if len(buf) > 0x7F {
		return []byte{0}
	}
	// #nosec G115 -- len(buf) is explicitly bounded to 0x7F.
	return append([]byte{0x80 | byte(len(buf))}, buf...)
}

func encodeInteger(v int) []byte {
	if v == 0 {
		return encodeTLV(0x02, []byte{0})
	}
	buf := make([]byte, 0, 4)
	for v > 0 {
		buf = append([]byte{byte(v & 0xFF)}, buf...)
		v >>= 8
	}
	if buf[0]&0x80 != 0 {
		buf = append([]byte{0x00}, buf...)
	}
	return encodeTLV(0x02, buf)
}

func encodeEnumerated(v int) []byte {
	if v == 0 {
		return encodeTLV(0x0A, []byte{0})
	}
	buf := make([]byte, 0, 4)
	for v > 0 {
		buf = append([]byte{byte(v & 0xFF)}, buf...)
		v >>= 8
	}
	if buf[0]&0x80 != 0 {
		buf = append([]byte{0x00}, buf...)
	}
	return encodeTLV(0x0A, buf)
}

func encodeBoolean(v bool) []byte {
	if v {
		return encodeTLV(0x01, []byte{0xFF})
	}
	return encodeTLV(0x01, []byte{0x00})
}

func encodeOctetString(v string) []byte {
	return encodeTLV(0x04, []byte(v))
}

func parseLDAPFilter(raw string) ([]byte, error) {
	filter, next, err := parseLDAPFilterAt(strings.TrimSpace(raw), 0)
	if err != nil {
		return nil, err
	}
	if next != len(strings.TrimSpace(raw)) {
		return nil, errors.New("unexpected ldap filter suffix")
	}
	return filter, nil
}

func parseLDAPFilterAt(raw string, start int) ([]byte, int, error) {
	if start >= len(raw) || raw[start] != '(' {
		return nil, 0, errors.New("ldap filter must start with '('")
	}
	pos := start + 1
	if pos >= len(raw) {
		return nil, 0, errors.New("truncated ldap filter")
	}

	switch raw[pos] {
	case '&', '|':
		op := raw[pos]
		pos++
		children := make([]byte, 0)
		for pos < len(raw) && raw[pos] == '(' {
			child, next, err := parseLDAPFilterAt(raw, pos)
			if err != nil {
				return nil, 0, err
			}
			children = append(children, child...)
			pos = next
		}
		if pos >= len(raw) || raw[pos] != ')' {
			return nil, 0, errors.New("unterminated ldap filter list")
		}
		tag := byte(0xA0)
		if op == '|' {
			tag = 0xA1
		}
		return encodeTLV(tag, children), pos + 1, nil
	default:
		equalsAt := strings.IndexByte(raw[pos:], '=')
		if equalsAt < 0 {
			return nil, 0, errors.New("ldap equality filter missing '='")
		}
		equalsAt += pos
		attr := strings.TrimSpace(raw[pos:equalsAt])
		end := equalsAt + 1
		for end < len(raw) && raw[end] != ')' {
			end++
		}
		if end >= len(raw) {
			return nil, 0, errors.New("unterminated ldap equality filter")
		}
		value := raw[equalsAt+1 : end]
		if value == "*" {
			return encodeTLV(0x87, []byte(attr)), end + 1, nil
		}
		decoded, err := unescapeLDAPFilterValue(value)
		if err != nil {
			return nil, 0, err
		}
		body := append(encodeOctetString(attr), encodeTLV(0x04, decoded)...)
		return encodeTLV(0xA3, body), end + 1, nil
	}
}

func escapeLDAPFilterValue(raw string) string {
	replacer := strings.NewReplacer(
		"\\", "\\5c",
		"*", "\\2a",
		"(", "\\28",
		")", "\\29",
		string(rune(0)), "\\00",
	)
	return replacer.Replace(raw)
}

func unescapeLDAPFilterValue(raw string) ([]byte, error) {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+2 >= len(raw) {
			out = append(out, raw[i])
			continue
		}
		decoded, err := hex.DecodeString(raw[i+1 : i+3])
		if err != nil || len(decoded) != 1 {
			return nil, errors.New("invalid ldap filter escape")
		}
		out = append(out, decoded[0])
		i += 2
	}
	return out, nil
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, item := range in {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func mapLDAPUserType(groupMapping map[string]string, groups []string) UserType {
	for _, group := range groups {
		group = strings.TrimSpace(group)
		if group == "" {
			continue
		}
		cn := ldapCommonName(group)
		for source, target := range groupMapping {
			if !strings.EqualFold(strings.TrimSpace(source), group) && !strings.EqualFold(strings.TrimSpace(source), cn) {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(target), "admin") {
				return UserTypeAdmin
			}
		}
	}
	return UserTypeVirtual
}

func ldapCommonName(group string) string {
	parts := strings.Split(group, ",")
	if len(parts) == 0 {
		return strings.TrimSpace(group)
	}
	first := strings.TrimSpace(parts[0])
	if strings.HasPrefix(strings.ToLower(first), "cn=") {
		return strings.TrimSpace(first[3:])
	}
	return strings.TrimSpace(first)
}

func renderLDAPHomeDir(template, username string) string {
	template = strings.TrimSpace(template)
	if template == "" {
		template = "/"
	}
	out := strings.ReplaceAll(template, "{username}", username)
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return out
}

func hostOnly(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}
