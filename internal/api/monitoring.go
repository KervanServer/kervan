package api

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/transfer"
)

type metricDefinition struct {
	help string
	kind string
}

type metricSample struct {
	name   string
	labels map[string]string
	value  float64
}

type metricsEmitter struct {
	defs    map[string]metricDefinition
	samples []metricSample
}

func newMetricsEmitter() *metricsEmitter {
	return &metricsEmitter{
		defs:    make(map[string]metricDefinition),
		samples: make([]metricSample, 0, 32),
	}
}

func (m *metricsEmitter) add(name, kind, help string, value float64, labels map[string]string) {
	if _, exists := m.defs[name]; !exists {
		m.defs[name] = metricDefinition{help: help, kind: kind}
	}
	m.samples = append(m.samples, metricSample{
		name:   name,
		labels: labels,
		value:  value,
	})
}

func (m *metricsEmitter) write(w io.Writer) {
	sort.Slice(m.samples, func(i, j int) bool {
		if m.samples[i].name == m.samples[j].name {
			return formatMetricLabels(m.samples[i].labels) < formatMetricLabels(m.samples[j].labels)
		}
		return m.samples[i].name < m.samples[j].name
	})

	written := make(map[string]struct{}, len(m.defs))
	for _, sample := range m.samples {
		if _, ok := written[sample.name]; !ok {
			def := m.defs[sample.name]
			_, _ = io.WriteString(w, "# HELP "+sample.name+" "+def.help+"\n")
			_, _ = io.WriteString(w, "# TYPE "+sample.name+" "+def.kind+"\n")
			written[sample.name] = struct{}{}
		}
		line := sample.name
		if labelText := formatMetricLabels(sample.labels); labelText != "" {
			line += "{" + labelText + "}"
		}
		line += " " + formatFloat(sample.value) + "\n"
		_, _ = io.WriteString(w, line)
	}
}

func formatMetricLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.ReplaceAll(labels[key], `\`, `\\`)
		value = strings.ReplaceAll(value, "\n", `\n`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		parts = append(parts, key+`="`+value+`"`)
	}
	return strings.Join(parts, ",")
}

func (s *Server) buildHealthResponse() map[string]any {
	snapshot := s.serverSnapshot()
	checks := map[string]any{
		"auth":            subsystemCheck(s.auth != nil, "local"),
		"user_repository": subsystemCheck(s.users != nil, "embedded"),
		"cobaltdb":        s.storeCheck(snapshot),
		"filesystem":      subsystemCheck(s.fsBuilder != nil, "user_vfs"),
		"audit":           s.auditCheck(),
		"storage":         storageCheck(snapshot),
	}

	ftpEnabled, _ := boolFromAny(snapshot["ftp_enabled"])
	ftpPort, _ := intFromAny(snapshot["ftp_port"])
	checks["ftp"] = listenerCheck(ftpEnabled, ftpPort, map[string]any{"protocol": "ftp"})

	ftpsEnabled, _ := boolFromAny(snapshot["ftps_enabled"])
	ftpsMode := stringFromAny(snapshot["ftps_mode"])
	ftpsImplicitPort, _ := intFromAny(snapshot["ftps_implicit_port"])
	checks["ftps"] = ftpsCheck(ftpsEnabled, ftpsMode, ftpPort, ftpsImplicitPort)

	sftpEnabled, _ := boolFromAny(snapshot["sftp_enabled"])
	sftpPort, _ := intFromAny(snapshot["sftp_port"])
	checks["sftp"] = listenerCheck(sftpEnabled, sftpPort, map[string]any{"protocol": "sftp"})

	scpEnabled, _ := boolFromAny(snapshot["scp_enabled"])
	checks["scp"] = listenerCheck(scpEnabled, sftpPort, map[string]any{"protocol": "scp"})

	webuiEnabled, _ := boolFromAny(snapshot["webui_enabled"])
	webuiPort, _ := intFromAny(snapshot["webui_port"])
	checks["webui"] = listenerCheck(webuiEnabled, webuiPort, map[string]any{"protocol": "http"})

	resp := map[string]any{
		"status":     summarizeHealth(checks),
		"checked_at": time.Now().UTC(),
		"checks":     checks,
	}
	copySnapshotField(resp, snapshot, "name")
	copySnapshotField(resp, snapshot, "version")
	copySnapshotField(resp, snapshot, "started_at")
	copySnapshotField(resp, snapshot, "uptime_seconds")
	if s.transfers != nil {
		resp["transfers"] = s.transfers.Stats()
	}
	return resp
}

func (s *Server) writeMetrics(w io.Writer) {
	emitter := newMetricsEmitter()
	snapshot := s.serverSnapshot()

	if s.sessions != nil {
		sessions := s.sessions.List()
		emitter.add(
			"kervan_sessions_active",
			"gauge",
			"Currently active authenticated sessions across all protocols.",
			float64(len(sessions)),
			nil,
		)

		activeByProtocol, totalByProtocol := s.sessions.ProtocolStats()
		for protocol, count := range activeByProtocol {
			emitter.add(
				"kervan_connections_active",
				"gauge",
				"Currently active connections grouped by protocol.",
				float64(count),
				map[string]string{"protocol": protocol},
			)
		}
		for protocol, count := range totalByProtocol {
			emitter.add(
				"kervan_connections_total",
				"counter",
				"Total connections accepted since process start grouped by protocol.",
				float64(count),
				map[string]string{"protocol": protocol},
			)
		}
	}

	if s.users != nil {
		users, err := s.users.List()
		if err == nil {
			totalUsers := 0
			adminUsers := 0
			enabledUsers := 0
			disabledUsers := 0
			lockedUsers := 0
			now := time.Now().UTC()
			for _, user := range users {
				if user == nil {
					continue
				}
				totalUsers++
				if user.Type == auth.UserTypeAdmin {
					adminUsers++
				}
				if user.Enabled {
					enabledUsers++
				} else {
					disabledUsers++
				}
				if user.LockedUntil != nil && user.LockedUntil.After(now) {
					lockedUsers++
				}
			}
			emitter.add("kervan_users_total", "gauge", "Total known users.", float64(totalUsers), nil)
			emitter.add("kervan_users_admin_total", "gauge", "Total admin users.", float64(adminUsers), nil)
			emitter.add("kervan_users_enabled_total", "gauge", "Total enabled users.", float64(enabledUsers), nil)
			emitter.add("kervan_users_disabled_total", "gauge", "Total disabled users.", float64(disabledUsers), nil)
			emitter.add("kervan_auth_locked_accounts", "gauge", "Currently locked accounts.", float64(lockedUsers), nil)
		}
	}

	if s.transfers != nil {
		stats := s.transfers.Stats()
		emitter.add("kervan_transfers_active", "gauge", "Currently active transfers.", float64(stats.ActiveTransfers), nil)
		emitter.add("kervan_transfers_total", "counter", "Total transfers started since process boot.", float64(stats.TotalTransfers), nil)
		emitter.add("kervan_transfers_completed_total", "counter", "Completed transfers since process boot.", float64(stats.Completed), nil)
		emitter.add("kervan_transfers_failed_total", "counter", "Failed transfers since process boot.", float64(stats.Failed), nil)
		emitter.add("kervan_transfer_upload_bytes_total", "counter", "Uploaded bytes since process boot.", float64(stats.UploadBytes), nil)
		emitter.add("kervan_transfer_download_bytes_total", "counter", "Downloaded bytes since process boot.", float64(stats.DownloadBytes), nil)
		emitter.add(
			"kervan_transfer_bytes_total",
			"counter",
			"Transferred bytes since process boot grouped by direction.",
			float64(stats.UploadBytes),
			map[string]string{"direction": string(transfer.DirectionUpload)},
		)
		emitter.add(
			"kervan_transfer_bytes_total",
			"counter",
			"Transferred bytes since process boot grouped by direction.",
			float64(stats.DownloadBytes),
			map[string]string{"direction": string(transfer.DirectionDownload)},
		)

		activeByProtocol := make(map[string]int)
		for _, tr := range s.transfers.Active() {
			if tr == nil {
				continue
			}
			activeByProtocol[tr.Protocol]++
		}
		for protocol, count := range activeByProtocol {
			emitter.add(
				"kervan_transfers_active_by_protocol",
				"gauge",
				"Currently active transfers grouped by protocol.",
				float64(count),
				map[string]string{"protocol": protocol},
			)
		}
	}

	if uptime, ok := floatFromAny(snapshot["uptime_seconds"]); ok {
		emitter.add("kervan_uptime_seconds", "gauge", "Process uptime in seconds.", uptime, nil)
	}
	emitter.add("kervan_goroutines", "gauge", "Current goroutine count.", float64(runtime.NumGoroutine()), nil)

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	emitter.add("kervan_memory_bytes", "gauge", "Currently allocated heap bytes.", float64(mem.Alloc), nil)

	emitter.write(w)
}

func (s *Server) serverSnapshot() map[string]any {
	if s.status == nil {
		return map[string]any{}
	}
	snapshot := s.status()
	if snapshot == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(snapshot))
	for key, value := range snapshot {
		out[key] = value
	}
	return out
}

func subsystemCheck(ok bool, component string) map[string]any {
	status := "up"
	if !ok {
		status = "down"
	}
	return map[string]any{
		"status":    status,
		"component": component,
	}
}

func listenerCheck(enabled bool, port int, extra map[string]any) map[string]any {
	out := cloneMap(extra)
	switch {
	case !enabled:
		out["status"] = "disabled"
	case port <= 0:
		out["status"] = "down"
		out["message"] = "listener port is not configured"
	default:
		out["status"] = "up"
		out["port"] = port
	}
	return out
}

func ftpsCheck(enabled bool, mode string, explicitPort, implicitPort int) map[string]any {
	out := map[string]any{
		"mode": mode,
	}
	if !enabled {
		out["status"] = "disabled"
		return out
	}

	ports := make([]int, 0, 2)
	if mode == "explicit" || mode == "both" {
		if explicitPort > 0 {
			ports = append(ports, explicitPort)
		}
	}
	if mode == "implicit" || mode == "both" {
		if implicitPort > 0 {
			ports = append(ports, implicitPort)
		}
	}
	if len(ports) == 0 {
		out["status"] = "down"
		out["message"] = "no FTPS listener ports are configured"
		return out
	}
	out["status"] = "up"
	out["ports"] = ports
	return out
}

func storageCheck(snapshot map[string]any) map[string]any {
	backend := stringFromAny(snapshot["storage_backend"])
	if backend == "" {
		backend = "local"
	}
	out := map[string]any{
		"backend": backend,
	}

	switch backend {
	case "memory":
		out["status"] = "up"
		out["message"] = "in-memory backend"
		return out
	case "local":
		root := stringFromAny(snapshot["storage_root"])
		out["root"] = root
		if strings.TrimSpace(root) == "" {
			out["status"] = "degraded"
			out["message"] = "local storage root is not configured"
			return out
		}
		info, err := os.Stat(root)
		if err != nil {
			out["status"] = "down"
			out["error"] = err.Error()
			return out
		}
		out["exists"] = true
		if !info.IsDir() {
			out["status"] = "down"
			out["message"] = "local storage root is not a directory"
			return out
		}
		out["status"] = "up"
		return out
	default:
		out["status"] = "degraded"
		out["message"] = "backend-specific health probe is not implemented"
		return out
	}
}

func (s *Server) storeCheck(snapshot map[string]any) map[string]any {
	out := map[string]any{
		"status": "up",
	}
	if s.store == nil {
		out["status"] = "down"
		return out
	}
	storePath := stringFromAny(snapshot["store_path"])
	if strings.TrimSpace(storePath) != "" {
		out["path"] = storePath
		dir := filepath.Dir(storePath)
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			out["status"] = "down"
			if err != nil {
				out["error"] = err.Error()
			}
		}
	}
	return out
}

func (s *Server) auditCheck() map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(s.auditLogPath) == "" {
		out["status"] = "disabled"
		return out
	}
	out["path"] = s.auditLogPath
	dir := filepath.Dir(s.auditLogPath)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		out["status"] = "down"
		if err != nil {
			out["error"] = err.Error()
		}
		return out
	}
	out["status"] = "up"
	return out
}

func summarizeHealth(checks map[string]any) string {
	required := map[string]struct{}{
		"auth":            {},
		"user_repository": {},
		"cobaltdb":        {},
		"filesystem":      {},
	}
	degraded := false
	for name, raw := range checks {
		check, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		status := stringFromAny(check["status"])
		switch status {
		case "down":
			if _, isRequired := required[name]; isRequired {
				return "unhealthy"
			}
			degraded = true
		case "degraded":
			degraded = true
		}
	}
	if degraded {
		return "degraded"
	}
	return "healthy"
}

func copySnapshotField(dst, snapshot map[string]any, key string) {
	if value, ok := snapshot[key]; ok {
		dst[key] = value
	}
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func stringFromAny(v any) string {
	switch typed := v.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func boolFromAny(v any) (bool, bool) {
	switch typed := v.(type) {
	case bool:
		return typed, true
	default:
		return false, false
	}
}

func intFromAny(v any) (int, bool) {
	switch typed := v.(type) {
	case int:
		return typed, true
	case int8:
		return int(typed), true
	case int16:
		return int(typed), true
	case int32:
		return int(typed), true
	case int64:
		return int(typed), true
	case uint:
		return int(typed), true
	case uint8:
		return int(typed), true
	case uint16:
		return int(typed), true
	case uint32:
		return int(typed), true
	case uint64:
		return int(typed), true
	case float32:
		return int(typed), true
	case float64:
		return int(typed), true
	default:
		return 0, false
	}
}

func floatFromAny(v any) (float64, bool) {
	switch typed := v.(type) {
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case float32:
		return float64(typed), true
	case float64:
		return typed, true
	default:
		return 0, false
	}
}
