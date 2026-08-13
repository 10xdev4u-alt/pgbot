// Package mcp is a tiny Model Context Protocol server over stdio — a third
// renderer over pgbot's Context, for AI agents. It speaks newline-delimited
// JSON-RPC 2.0 (the MCP stdio transport) with only the standard library, so it
// costs no dependencies and keeps the static binary. The server exposes
// DETERMINISTIC tools only; the connected model does the explaining.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
)

// Tool is one callable an agent can invoke. Handler returns the tool's text
// result (usually JSON); an error becomes an MCP tool error, not a transport
// error, so a bad DSN never kills the session.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any // JSON Schema for the arguments object
	Handler     func(ctx context.Context, args json.RawMessage) (string, error)
}

// Server serves a fixed set of tools.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Tools        []Tool
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent ⇒ notification (no reply)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// defaultProtocol is the MCP revision we advertise when a client doesn't pin one.
const defaultProtocol = "2024-11-05"

// Serve runs the read-dispatch-write loop until stdin closes. Messages are one
// JSON object per line; responses go to out. Nothing but protocol goes to out —
// callers must log to stderr.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	w := bufio.NewWriter(out)
	proto := defaultProtocol
	for {
		line, err := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			s.dispatch(ctx, line, w, &proto)
			_ = w.Flush()
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func (s *Server) dispatch(ctx context.Context, raw []byte, w io.Writer, proto *string) {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeErr(w, nil, -32700, "parse error")
		return
	}
	isNotification := len(req.ID) == 0

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		if p.ProtocolVersion != "" {
			*proto = p.ProtocolVersion // echo the client's revision for compatibility
		}
		writeResult(w, req.ID, map[string]any{
			"protocolVersion": *proto,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
			"instructions":    s.Instructions,
		})

	case "notifications/initialized", "notifications/cancelled":
		// notifications: no reply

	case "ping":
		writeResult(w, req.ID, map[string]any{})

	case "tools/list":
		writeResult(w, req.ID, map[string]any{"tools": s.toolDescriptors()})

	case "tools/call":
		s.callTool(ctx, req, w)

	default:
		if !isNotification {
			writeErr(w, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *Server) toolDescriptors() []map[string]any {
	out := make([]map[string]any, 0, len(s.Tools))
	for _, t := range s.Tools {
		schema := t.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return out
}

func (s *Server) callTool(ctx context.Context, req rpcRequest, w io.Writer) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeErr(w, req.ID, -32602, "invalid params")
		return
	}
	var tool *Tool
	for i := range s.Tools {
		if s.Tools[i].Name == p.Name {
			tool = &s.Tools[i]
			break
		}
	}
	if tool == nil {
		writeErr(w, req.ID, -32602, "unknown tool: "+p.Name)
		return
	}
	// A tool failure is reported as a tool result with isError=true (the model
	// sees it), NOT a JSON-RPC error — the session keeps going.
	text, err := tool.Handler(ctx, p.Arguments)
	if err != nil {
		writeResult(w, req.ID, toolResult("error: "+err.Error(), true))
		return
	}
	writeResult(w, req.ID, toolResult(text, false))
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func writeResult(w io.Writer, id json.RawMessage, result any) {
	writeMessage(w, rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Result: result})
}

func writeErr(w io.Writer, id json.RawMessage, code int, msg string) {
	writeMessage(w, rpcResponse{JSONRPC: "2.0", ID: idOrNull(id), Error: &rpcError{Code: code, Message: msg}})
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeMessage(w io.Writer, msg rpcResponse) {
	b, err := json.Marshal(msg)
	if err != nil {
		return
	}
	b = append(b, '\n') // newline-delimited transport
	_, _ = w.Write(b)
}
