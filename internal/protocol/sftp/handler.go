package sftp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/transfer"
	"github.com/kervanserver/kervan/internal/vfs"
	"golang.org/x/crypto/ssh"
)

const (
	fxpInit     = 1
	fxpVersion  = 2
	fxpOpen     = 3
	fxpClose    = 4
	fxpRead     = 5
	fxpWrite    = 6
	fxpLstat    = 7
	fxpFstat    = 8
	fxpSetstat  = 9
	fxpFsetstat = 10
	fxpOpendir  = 11
	fxpReaddir  = 12
	fxpRemove   = 13
	fxpMkdir    = 14
	fxpRmdir    = 15
	fxpRealpath = 16
	fxpStat     = 17
	fxpRename   = 18
	fxpReadlink = 19
	fxpSymlink  = 20

	fxpStatus   = 101
	fxpHandle   = 102
	fxpData     = 103
	fxpName     = 104
	fxpAttrs    = 105
	fxpExtended = 200

	fxOK               = 0
	fxEOF              = 1
	fxNoSuchFile       = 2
	fxPermissionDenied = 3
	fxFailure          = 4
	fxBadMessage       = 5
	fxOpUnsupported    = 8

	sshFxRead   = 0x00000001
	sshFxWrite  = 0x00000002
	sshFxAppend = 0x00000004
	sshFxCreat  = 0x00000008
	sshFxTrunc  = 0x00000010
	sshFxExcl   = 0x00000020
)

const maxPacketSize = 16 * 1024 * 1024

type sftpHandler struct {
	ch       ssh.Channel
	fsys     vfs.FileSystem
	logger   func(msg string, kv ...any)
	audit    *audit.Engine
	xfer     *transfer.Manager
	username string
	remoteIP string

	handles    map[string]any
	nextHandle uint64
}

type openFile struct {
	path       string
	file       vfs.File
	transferID string
}

type openDir struct {
	path    string
	entries []fs.DirEntry
	idx     int
}

func (s *Server) runSFTP(ch ssh.Channel, fsys vfs.FileSystem, username, remoteAddr string) {
	handler := &sftpHandler{
		ch:       ch,
		fsys:     fsys,
		audit:    s.audit,
		xfer:     s.xfer,
		username: username,
		remoteIP: remoteAddr,
		handles:  make(map[string]any),
		logger: func(msg string, kv ...any) {
			if s.logger != nil {
				s.logger.Debug(msg, kv...)
			}
		},
	}
	_ = handler.loop()
}

func (h *sftpHandler) loop() error {
	defer h.closeAllHandles()
	for {
		packetType, payload, err := readPacket(h.ch)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		switch packetType {
		case fxpInit:
			if err := h.handleInit(payload); err != nil {
				return err
			}
		case fxpOpen:
			_ = h.handleOpen(payload)
		case fxpClose:
			_ = h.handleClose(payload)
		case fxpRead:
			_ = h.handleRead(payload)
		case fxpWrite:
			_ = h.handleWrite(payload)
		case fxpOpendir:
			_ = h.handleOpenDir(payload)
		case fxpReaddir:
			_ = h.handleReadDir(payload)
		case fxpRemove:
			_ = h.handleRemove(payload)
		case fxpMkdir:
			_ = h.handleMkdir(payload)
		case fxpRmdir:
			_ = h.handleRmdir(payload)
		case fxpStat:
			_ = h.handleStat(payload, false)
		case fxpLstat:
			_ = h.handleStat(payload, true)
		case fxpFstat:
			_ = h.handleFStat(payload)
		case fxpRename:
			_ = h.handleRename(payload)
		case fxpRealpath:
			_ = h.handleRealpath(payload)
		case fxpSetstat, fxpFsetstat, fxpReadlink, fxpSymlink, fxpExtended:
			_ = h.replyStatus(idFromPayload(payload), fxOpUnsupported, "operation unsupported")
		default:
			_ = h.replyStatus(idFromPayload(payload), fxBadMessage, "unknown packet")
		}
	}
}

func (h *sftpHandler) handleInit(payload []byte) error {
	r := packetReader{buf: payload}
	_, err := r.uint32()
	if err != nil {
		return err
	}
	var body packetWriter
	body.uint32(3)
	return writePacket(h.ch, fxpVersion, body.buf)
}

func (h *sftpHandler) handleOpen(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad open path")
	}
	p = h.normalizePath(p)
	pflags, err := r.uint32()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad open flags")
	}
	if err := r.skipAttrs(); err != nil {
		return h.replyStatus(id, fxBadMessage, "bad attrs")
	}

	file, openErr := h.fsys.Open(p, mapOpenFlags(pflags), 0o644)
	if openErr != nil {
		return h.replyStatus(id, mapStatus(openErr), openErr.Error())
	}
	transferID := ""
	if h.xfer != nil {
		dir := transfer.DirectionDownload
		if pflags&(sshFxWrite|sshFxAppend|sshFxCreat|sshFxTrunc) != 0 {
			dir = transfer.DirectionUpload
		}
		transferID = h.xfer.Start(h.username, "sftp", p, dir, -1)
	}
	handle := h.newHandle()
	h.handles[handle] = &openFile{path: p, file: file, transferID: transferID}

	var body packetWriter
	body.uint32(id)
	body.string(handle)
	return writePacket(h.ch, fxpHandle, body.buf)
}

func (h *sftpHandler) handleClose(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	handle, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad handle")
	}
	entry, ok := h.handles[handle]
	if !ok {
		return h.replyStatus(id, fxFailure, "invalid handle")
	}
	switch v := entry.(type) {
	case *openFile:
		_ = v.file.Close()
		if h.xfer != nil && v.transferID != "" {
			h.xfer.End(v.transferID, transfer.StatusCompleted, "")
		}
	case *openDir:
		_ = v
	}
	delete(h.handles, handle)
	return h.replyStatus(id, fxOK, "ok")
}

func (h *sftpHandler) handleRead(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	handle, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad handle")
	}
	offset, err := r.uint64()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad offset")
	}
	length, err := r.uint32()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad length")
	}

	entry, ok := h.handles[handle]
	if !ok {
		return h.replyStatus(id, fxFailure, "invalid handle")
	}
	of, ok := entry.(*openFile)
	if !ok {
		return h.replyStatus(id, fxFailure, "handle is not file")
	}

	buf := make([]byte, length)
	n, readErr := of.file.ReadAt(buf, int64(offset))
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return h.replyStatus(id, mapStatus(readErr), readErr.Error())
	}
	if n == 0 && errors.Is(readErr, io.EOF) {
		return h.replyStatus(id, fxEOF, "eof")
	}
	if h.xfer != nil && of.transferID != "" {
		h.xfer.AddBytes(of.transferID, int64(n))
	}

	var body packetWriter
	body.uint32(id)
	body.bytes(buf[:n])
	return writePacket(h.ch, fxpData, body.buf)
}

func (h *sftpHandler) handleWrite(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	handle, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad handle")
	}
	offset, err := r.uint64()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad offset")
	}
	data, err := r.rawString()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad data")
	}

	entry, ok := h.handles[handle]
	if !ok {
		return h.replyStatus(id, fxFailure, "invalid handle")
	}
	of, ok := entry.(*openFile)
	if !ok {
		return h.replyStatus(id, fxFailure, "handle is not file")
	}
	if _, err := of.file.Seek(int64(offset), io.SeekStart); err != nil {
		return h.replyStatus(id, fxFailure, err.Error())
	}
	if _, err := of.file.Write(data); err != nil {
		if h.xfer != nil && of.transferID != "" {
			h.xfer.End(of.transferID, transfer.StatusFailed, err.Error())
			of.transferID = ""
		}
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	if h.xfer != nil && of.transferID != "" {
		h.xfer.AddBytes(of.transferID, int64(len(data)))
	}
	if h.audit != nil {
		h.audit.Emit(audit.Event{
			Type:     audit.EventFileWrite,
			Username: h.username,
			Protocol: "sftp",
			Path:     of.path,
			IP:       h.remoteIP,
			Status:   "ok",
			Message:  "write",
		})
	}
	return h.replyStatus(id, fxOK, "ok")
}

func (h *sftpHandler) handleOpenDir(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad path")
	}
	p = h.normalizePath(p)
	entries, err := h.fsys.ReadDir(p)
	if err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	handle := h.newHandle()
	h.handles[handle] = &openDir{
		path:    p,
		entries: entries,
	}
	var body packetWriter
	body.uint32(id)
	body.string(handle)
	return writePacket(h.ch, fxpHandle, body.buf)
}

func (h *sftpHandler) handleReadDir(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	handle, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad handle")
	}
	entry, ok := h.handles[handle]
	if !ok {
		return h.replyStatus(id, fxFailure, "invalid handle")
	}
	dir, ok := entry.(*openDir)
	if !ok {
		return h.replyStatus(id, fxFailure, "handle is not directory")
	}
	if dir.idx >= len(dir.entries) {
		return h.replyStatus(id, fxEOF, "eof")
	}
	count := len(dir.entries) - dir.idx
	if count > 100 {
		count = 100
	}

	var body packetWriter
	body.uint32(id)
	body.uint32(uint32(count))
	for i := 0; i < count; i++ {
		de := dir.entries[dir.idx+i]
		info, infoErr := de.Info()
		if infoErr != nil {
			continue
		}
		body.string(de.Name())
		body.string(formatLongname(info))
		body.bytes(marshalAttrs(info))
	}
	dir.idx += count
	return writePacket(h.ch, fxpName, body.buf)
}

func (h *sftpHandler) handleRemove(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad path")
	}
	p = h.normalizePath(p)
	if err := h.fsys.Remove(p); err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	if h.audit != nil {
		h.audit.Emit(audit.Event{
			Type:     audit.EventFileDelete,
			Username: h.username,
			Protocol: "sftp",
			Path:     p,
			IP:       h.remoteIP,
			Status:   "ok",
			Message:  "remove",
		})
	}
	return h.replyStatus(id, fxOK, "ok")
}

func (h *sftpHandler) handleMkdir(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad path")
	}
	p = h.normalizePath(p)
	if err := r.skipAttrs(); err != nil {
		return h.replyStatus(id, fxBadMessage, "bad attrs")
	}
	if err := h.fsys.Mkdir(p, 0o755); err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	return h.replyStatus(id, fxOK, "ok")
}

func (h *sftpHandler) handleRmdir(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad path")
	}
	p = h.normalizePath(p)
	if err := h.fsys.Remove(p); err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	return h.replyStatus(id, fxOK, "ok")
}

func (h *sftpHandler) handleStat(payload []byte, lstat bool) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad path")
	}
	p = h.normalizePath(p)
	var info os.FileInfo
	if lstat {
		info, err = h.fsys.Lstat(p)
	} else {
		info, err = h.fsys.Stat(p)
	}
	if err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}

	var body packetWriter
	body.uint32(id)
	body.bytes(marshalAttrs(info))
	return writePacket(h.ch, fxpAttrs, body.buf)
}

func (h *sftpHandler) handleFStat(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	handle, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad handle")
	}
	entry, ok := h.handles[handle]
	if !ok {
		return h.replyStatus(id, fxFailure, "invalid handle")
	}
	file, ok := entry.(*openFile)
	if !ok {
		return h.replyStatus(id, fxFailure, "handle is not file")
	}
	info, err := file.file.Stat()
	if err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	var body packetWriter
	body.uint32(id)
	body.bytes(marshalAttrs(info))
	return writePacket(h.ch, fxpAttrs, body.buf)
}

func (h *sftpHandler) handleRename(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	oldName, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad old path")
	}
	oldName = h.normalizePath(oldName)
	newName, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad new path")
	}
	newName = h.normalizePath(newName)
	if err := h.fsys.Rename(oldName, newName); err != nil {
		return h.replyStatus(id, mapStatus(err), err.Error())
	}
	return h.replyStatus(id, fxOK, "ok")
}

func (h *sftpHandler) handleRealpath(payload []byte) error {
	r := packetReader{buf: payload}
	id, err := r.uint32()
	if err != nil {
		return err
	}
	p, err := r.string()
	if err != nil {
		return h.replyStatus(id, fxBadMessage, "bad path")
	}
	p = h.normalizePath(p)

	var body packetWriter
	body.uint32(id)
	body.uint32(1)
	body.string(p)
	body.string(p)

	info, statErr := h.fsys.Stat(p)
	if statErr == nil {
		body.bytes(marshalAttrs(info))
	} else {
		var attrs packetWriter
		attrs.uint32(0)
		body.bytes(attrs.buf)
	}
	return writePacket(h.ch, fxpName, body.buf)
}

func (h *sftpHandler) replyStatus(id uint32, code uint32, message string) error {
	var body packetWriter
	body.uint32(id)
	body.uint32(code)
	body.string(message)
	body.string("")
	return writePacket(h.ch, fxpStatus, body.buf)
}

func (h *sftpHandler) newHandle() string {
	h.nextHandle++
	return fmt.Sprintf("H%016x", h.nextHandle)
}

func (h *sftpHandler) closeAllHandles() {
	for k, v := range h.handles {
		switch e := v.(type) {
		case *openFile:
			_ = e.file.Close()
			if h.xfer != nil && e.transferID != "" {
				h.xfer.End(e.transferID, transfer.StatusFailed, "connection closed")
			}
		case *openDir:
			_ = e
		}
		delete(h.handles, k)
	}
}

type packetReader struct {
	buf []byte
	pos int
}

func (r *packetReader) uint32() (uint32, error) {
	if r.pos+4 > len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint32(r.buf[r.pos : r.pos+4])
	r.pos += 4
	return v, nil
}

func (r *packetReader) uint64() (uint64, error) {
	if r.pos+8 > len(r.buf) {
		return 0, io.ErrUnexpectedEOF
	}
	v := binary.BigEndian.Uint64(r.buf[r.pos : r.pos+8])
	r.pos += 8
	return v, nil
}

func (r *packetReader) rawString() ([]byte, error) {
	n, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if int(n) < 0 || r.pos+int(n) > len(r.buf) {
		return nil, io.ErrUnexpectedEOF
	}
	out := r.buf[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *packetReader) string() (string, error) {
	b, err := r.rawString()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *packetReader) skipAttrs() error {
	flags, err := r.uint32()
	if err != nil {
		return err
	}
	if flags&0x00000001 != 0 {
		if _, err := r.uint64(); err != nil {
			return err
		}
	}
	if flags&0x00000002 != 0 {
		if _, err := r.uint32(); err != nil {
			return err
		}
		if _, err := r.uint32(); err != nil {
			return err
		}
	}
	if flags&0x00000004 != 0 {
		if _, err := r.uint32(); err != nil {
			return err
		}
	}
	if flags&0x00000008 != 0 {
		if _, err := r.uint32(); err != nil {
			return err
		}
		if _, err := r.uint32(); err != nil {
			return err
		}
	}
	if flags&0x80000000 != 0 {
		count, err := r.uint32()
		if err != nil {
			return err
		}
		for i := uint32(0); i < count; i++ {
			if _, err := r.rawString(); err != nil {
				return err
			}
			if _, err := r.rawString(); err != nil {
				return err
			}
		}
	}
	return nil
}

type packetWriter struct {
	buf []byte
}

func (w *packetWriter) uint32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

func (w *packetWriter) uint64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	w.buf = append(w.buf, b[:]...)
}

func (w *packetWriter) string(s string) { w.bytes([]byte(s)) }

func (w *packetWriter) bytes(b []byte) {
	w.uint32(uint32(len(b)))
	w.buf = append(w.buf, b...)
}

func readPacket(r io.Reader) (byte, []byte, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > maxPacketSize {
		return 0, nil, errors.New("invalid packet size")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return payload[0], payload[1:], nil
}

func writePacket(w io.Writer, packetType byte, payload []byte) error {
	total := uint32(1 + len(payload))
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], total)
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if _, err := w.Write([]byte{packetType}); err != nil {
		return err
	}
	if len(payload) > 0 {
		_, err := w.Write(payload)
		return err
	}
	return nil
}

func mapOpenFlags(flags uint32) int {
	var out int
	if flags&sshFxRead != 0 && flags&sshFxWrite == 0 {
		out |= os.O_RDONLY
	}
	if flags&sshFxWrite != 0 {
		out |= os.O_WRONLY
	}
	if flags&sshFxRead != 0 && flags&sshFxWrite != 0 {
		out = os.O_RDWR
	}
	if flags&sshFxAppend != 0 {
		out |= os.O_APPEND
	}
	if flags&sshFxCreat != 0 {
		out |= os.O_CREATE
	}
	if flags&sshFxTrunc != 0 {
		out |= os.O_TRUNC
	}
	if flags&sshFxExcl != 0 {
		out |= os.O_EXCL
	}
	if out == 0 {
		out = os.O_RDONLY
	}
	return out
}

func mapStatus(err error) uint32 {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fxNoSuchFile
	case errors.Is(err, os.ErrPermission):
		return fxPermissionDenied
	case errors.Is(err, io.EOF):
		return fxEOF
	default:
		return fxFailure
	}
}

func marshalAttrs(info os.FileInfo) []byte {
	const (
		attrSize        = 0x00000001
		attrPermissions = 0x00000004
		attrACModTime   = 0x00000008
	)

	var w packetWriter
	w.uint32(attrSize | attrPermissions | attrACModTime)
	w.uint64(uint64(info.Size()))
	perms := uint32(info.Mode().Perm())
	if info.IsDir() {
		perms |= 0o040000
	} else {
		perms |= 0o100000
	}
	w.uint32(perms)
	secs := uint32(max(0, info.ModTime().Unix()))
	w.uint32(secs)
	w.uint32(secs)
	return w.buf
}

func formatLongname(info os.FileInfo) string {
	mode := "-rw-r--r--"
	if info.IsDir() {
		mode = "drwxr-xr-x"
	}
	return fmt.Sprintf("%s 1 owner group %12d %s %s", mode, info.Size(), info.ModTime().Format("Jan _2 15:04"), info.Name())
}

func idFromPayload(payload []byte) uint32 {
	if len(payload) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(payload[:4])
}

func max(a int64, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (h *sftpHandler) normalizePath(in string) string {
	if in == "" {
		return "/"
	}
	clean := path.Clean("/" + strings.TrimSpace(in))
	if clean == "." {
		return "/"
	}
	return clean
}
