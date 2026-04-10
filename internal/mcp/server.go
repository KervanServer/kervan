package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kervanserver/kervan/internal/auth"
	"github.com/kervanserver/kervan/internal/config"
	"github.com/kervanserver/kervan/internal/transfer"
)

const protocolVersion = "2024-11-05"

type Server struct {
	cfg       *config.Config
	repo      *auth.UserRepository
	auditLog  string
	transfers *transfer.Manager
	reader    *bufio.Reader
	writer    io.Writer
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func NewServer(cfg *config.Config, repo *auth.UserRepository, transfers *transfer.Manager, auditLog string, stdin io.Reader, stdout io.Writer) *Server {
	return &Server{
		cfg:       cfg,
		repo:      repo,
		transfers: transfers,
		auditLog:  auditLog,
		reader:    bufio.NewReader(stdin),
		writer:    stdout,
	}
}

func (s *Server) Serve(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		payload, err := readFrame(s.reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		var req request
		if err := json.Unmarshal(payload, &req); err != nil {
			if writeErr := s.writeResponse(response{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "parse error"},
			}); writeErr != nil {
				return writeErr
			}
			continue
		}

		if req.ID == nil {
			_ = s.handleNotification(req)
			continue
		}

		resp := response{JSONRPC: "2.0", ID: req.ID}
		result, rpcErr := s.handleRequest(ctx, req)
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			resp.Result = result
		}
		if err := s.writeResponse(resp); err != nil {
			return err
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req request) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "kervan-mcp",
				"version": "0.1.0",
			},
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": s.toolDefinitions()}, nil
	case "tools/call":
		return s.handleToolCall(ctx, req.Params)
	case "resources/list":
		return map[string]any{"resources": s.resourceDefinitions()}, nil
	case "resources/read":
		return s.handleResourceRead(req.Params)
	case "prompts/list":
		return map[string]any{"prompts": []any{}}, nil
	default:
		return nil, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) handleNotification(req request) error {
	switch req.Method {
	case "notifications/initialized":
		return nil
	default:
		return nil
	}
}

func (s *Server) writeResponse(resp response) error {
	raw, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n", len(raw))
	if err != nil {
		return err
	}
	_, err = s.writer.Write(raw)
	return err
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			_, err := fmt.Sscanf(line, "Content-Length: %d", &contentLength)
			if err != nil {
				_, err = fmt.Sscanf(line, "content-length: %d", &contentLength)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func makeTextResult(v any) (map[string]any, *rpcError) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "failed to encode result"}
	}
	return map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": string(raw),
			},
		},
	}, nil
}

func decodeParams[T any](raw json.RawMessage) (T, error) {
	var zero T
	if len(bytes.TrimSpace(raw)) == 0 {
		return zero, nil
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}
	return out, nil
}
