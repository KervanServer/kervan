package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kervanserver/kervan/internal/audit"
	"github.com/kervanserver/kervan/internal/config"
)

// errAuditTampered makes "kervan audit verify" exit non-zero on findings.
var errAuditTampered = errors.New("audit log failed verification")

func runAuditCommand(stdout io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: kervan audit verify [--config kervan.yaml] [--file audit.jsonl] [--key-file audit.key] [--json]")
	}
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	configPath := fs.String("config", defaultConfigPath, "Path to config file")
	filePath := fs.String("file", "", "Audit log to verify (default: the configured file output)")
	keyPath := fs.String("key-file", "", "HMAC key (default: audit.integrity.key_file or <data_dir>/audit.key)")
	jsonOut := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse audit verify flags: %w", err)
	}

	logPath, key := strings.TrimSpace(*filePath), strings.TrimSpace(*keyPath)
	if logPath == "" || key == "" {
		cfg, err := config.Load(*configPath)
		if err != nil {
			return fmt.Errorf("load config %s: %w", *configPath, err)
		}
		if logPath == "" {
			logPath = backupAuditPath(cfg)
		}
		if key == "" {
			key = cfg.AuditKeyPath()
		}
	}
	// Verification must never create a key: a missing key is an error.
	if _, err := os.Stat(key); err != nil {
		return fmt.Errorf("audit integrity key %s: %w", key, err)
	}
	keyBytes, err := audit.LoadOrCreateChainKey(key)
	if err != nil {
		return err
	}
	// #nosec G304 -- operator-supplied path.
	f, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	report, err := audit.VerifyChain(f, keyBytes)
	if err != nil {
		return fmt.Errorf("read audit log: %w", err)
	}

	if *jsonOut {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return err
		}
	} else {
		_, _ = fmt.Fprintf(stdout, "Audit log: %s\n", logPath)
		_, _ = fmt.Fprintf(stdout, "Records:   %d (%d chained, %d unchained, %d segment(s))\n", report.Records, report.Chained, report.Unchained, report.Segments)
		if report.LastSeq > 0 {
			_, _ = fmt.Fprintf(stdout, "Head:      seq %d mac %s\n", report.LastSeq, report.LastMAC)
		}
		for _, p := range report.Problems {
			seq := ""
			if p.Seq > 0 {
				seq = fmt.Sprintf(" (seq %d)", p.Seq)
			}
			_, _ = fmt.Fprintf(stdout, "PROBLEM line %d%s: %s\n", p.Line, seq, p.Reason)
		}
		if report.OK() {
			_, _ = fmt.Fprintln(stdout, "Result:    OK")
		} else {
			_, _ = fmt.Fprintln(stdout, "Result:    FAILED")
		}
	}
	if !report.OK() {
		return errAuditTampered
	}
	return nil
}
