package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/go-chi/chi/v5"
)

func (s *Server) handleWSCanvas(w http.ResponseWriter, r *http.Request) {
	projectID := chi.URLParam(r, "projectId")
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ch, unsub := s.hub.Subscribe(projectID)
	defer unsub()

	ctx := conn.CloseRead(r.Context())
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-ch:
			if err := wsjson.Write(ctx, conn, evt); err != nil {
				return
			}
		}
	}
}

func (s *Server) handleWSDeploymentLogs(w http.ResponseWriter, r *http.Request) {
	deploymentID := chi.URLParam(r, "deploymentId")
	if s.pipeline == nil {
		writeError(w, http.StatusServiceUnavailable, "NO_PIPELINE", "파이프라인이 없습니다.")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := conn.CloseRead(r.Context())
	ch, err := s.pipeline.SubscribeLogs(ctx, deploymentID)
	if err != nil {
		_ = wsjson.Write(ctx, conn, map[string]any{"error": err.Error(), "done": true})
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			if err := wsjson.Write(ctx, conn, evt); err != nil {
				return
			}
			if evt.Done {
				return
			}
		}
	}
}

func (s *Server) handleWSServiceLogs(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil || svc.ActiveContainerID == "" || s.engine == nil {
		writeError(w, http.StatusNotFound, "NO_ACTIVE_CONTAINER", "실행 중인 컨테이너가 없습니다.")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx := conn.CloseRead(r.Context())
	rc, err := s.engine.StreamLogs(ctx, svc.ActiveContainerID, "200", true)
	if err != nil {
		_ = wsjson.Write(ctx, conn, map[string]any{"error": err.Error()})
		return
	}
	defer rc.Close()

	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := wsjson.Write(ctx, conn, map[string]any{"line": scanner.Text()}); err != nil {
			return
		}
	}
}

func (s *Server) handleWSTerminal(w http.ResponseWriter, r *http.Request) {
	serviceID := chi.URLParam(r, "serviceId")
	svc, err := s.store.GetService(serviceID)
	if err != nil || svc.ActiveContainerID == "" || s.engine == nil {
		writeError(w, http.StatusNotFound, "NO_ACTIVE_CONTAINER", "실행 중인 컨테이너가 없습니다.")
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	sess, err := s.engine.OpenExec(ctx, svc.ActiveContainerID, nil, 28, 100)
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, []byte("\r\n[터미널 연결 실패: "+err.Error()+"]\r\n"))
		return
	}
	defer sess.Close()

	// Forward PTY output -> WebSocket
	go func() {
		defer cancel()
		buf := make([]byte, 4096)
		for {
			n, readErr := sess.Reader.Read(buf)
			if n > 0 {
				if writeErr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); writeErr != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Forward WebSocket input & resize JSON -> container PTY
	for {
		msgType, data, readErr := conn.Read(ctx)
		if readErr != nil {
			return
		}
		if msgType == websocket.MessageText && len(data) > 0 && data[0] == '{' {
			var ctrl struct {
				Type string `json:"type"`
				Data string `json:"data"`
				Rows uint   `json:"rows"`
				Cols uint   `json:"cols"`
			}
			if json.Unmarshal(data, &ctrl) == nil {
				switch ctrl.Type {
				case "resize":
					_ = sess.Resize(ctrl.Rows, ctrl.Cols)
					continue
				case "input":
					_, _ = sess.Conn.Write([]byte(ctrl.Data))
					continue
				}
			}
		}
		if len(data) > 0 {
			_, _ = sess.Conn.Write(data)
		}
	}
}
