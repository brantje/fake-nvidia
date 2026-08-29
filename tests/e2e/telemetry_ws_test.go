//go:build e2e

package e2e

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type runtimeTelemetryEvent struct {
	Type      string                   `json:"type"`
	Telemetry []runtimeTelemetrySample `json:"telemetry"`
}

type runtimeTelemetrySample struct {
	InstanceID        string             `json:"instance_id"`
	PID               int                `json:"pid"`
	GPUDevices        []string           `json:"gpu_devices"`
	GPUs              []runtimeGPUUsage  `json:"gpus"`
	VRAMUsedBytes     *int64             `json:"vram_used_bytes"`
	GPUUtilizationPct *float64           `json:"gpu_utilization_pct"`
}

type runtimeGPUUsage struct {
	DeviceID       string   `json:"device_id"`
	VRAMUsedBytes  *int64   `json:"vram_used_bytes"`
	UtilizationPct *float64 `json:"utilization_pct"`
}

func TestPhase8PerInstanceTelemetryWebSocket(t *testing.T) {
	h := startManager(t, gpuScenario{
		profile: "rtx4060ti-16gb", count: 1, vramMiB: 16 * 1024,
		extraEnv: map[string]string{
			"FAKE_LLAMA_SM_UTIL":     "63",
			"FAKE_LLAMA_MEMORY_UTIL": "27",
		},
	})
	modelID := h.createSparseModel("telemetry-websocket", 4*gib)
	instanceID := h.createInstance(modelID, "telemetry-websocket-worker", true)
	started := h.startInstance(instanceID, http.StatusAccepted)
	if started.State != "READY" || started.PID <= 0 {
		t.Fatalf("runtime after start=%+v", started)
	}

	ticket := h.websocketTicket()
	conn, reader := h.openWebSocket(ticket)
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}

	for {
		payload, err := readServerWebSocketText(reader)
		if err != nil {
			t.Fatalf("read manager telemetry websocket: %v", err)
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(payload, &envelope); err != nil || envelope.Type != "runtime_telemetry" {
			continue
		}
		var event runtimeTelemetryEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatalf("decode runtime telemetry: %v: %s", err, payload)
		}
		for _, sample := range event.Telemetry {
			if sample.InstanceID != instanceID {
				continue
			}
			if sample.PID != started.PID {
				t.Fatalf("telemetry PID=%d want=%d: %+v", sample.PID, started.PID, sample)
			}
			if len(sample.GPUDevices) != 1 || sample.GPUDevices[0] != "CUDA0" {
				t.Fatalf("telemetry devices=%v want [CUDA0]", sample.GPUDevices)
			}
			if sample.VRAMUsedBytes == nil || *sample.VRAMUsedBytes != 4*gib {
				t.Fatalf("telemetry VRAM=%v want=%d", sample.VRAMUsedBytes, 4*gib)
			}
			if sample.GPUUtilizationPct == nil || int(*sample.GPUUtilizationPct) != 63 {
				t.Fatalf("telemetry GPU utilization=%v want=63", sample.GPUUtilizationPct)
			}
			if len(sample.GPUs) != 1 || sample.GPUs[0].DeviceID != "CUDA0" || sample.GPUs[0].UtilizationPct == nil || int(*sample.GPUs[0].UtilizationPct) != 63 {
				t.Fatalf("per-device telemetry=%+v", sample.GPUs)
			}
			return
		}
	}
}

func (h *managerHarness) websocketTicket() string {
	h.t.Helper()
	status, body := h.rawRequest(http.MethodPost, "/api/v1/auth/ws-ticket", nil, true)
	if status != http.StatusCreated {
		h.t.Fatalf("websocket ticket status=%d body=%s", status, body)
	}
	var response struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		h.t.Fatalf("decode websocket ticket: %v: %s", err, body)
	}
	if strings.TrimSpace(response.Ticket) == "" {
		h.t.Fatalf("empty websocket ticket: %s", body)
	}
	return response.Ticket
}

func (h *managerHarness) openWebSocket(ticket string) (net.Conn, *bufio.Reader) {
	h.t.Helper()
	base, err := url.Parse(h.baseURL)
	if err != nil {
		h.t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", base.Host, 5*time.Second)
	if err != nil {
		h.t.Fatalf("dial manager websocket: %v", err)
	}
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		conn.Close()
		h.t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	path := "/api/v1/ws?ticket=" + url.QueryEscape(ticket)
	request := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, base.Host, key)
	if _, err := io.WriteString(conn, request); err != nil {
		conn.Close()
		h.t.Fatalf("write websocket handshake: %v", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		conn.Close()
		h.t.Fatalf("read websocket handshake: %v", err)
	}
	if response.Body != nil {
		defer response.Body.Close()
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		conn.Close()
		h.t.Fatalf("websocket handshake status=%d", response.StatusCode)
	}
	return conn, reader
}

func readServerWebSocketText(reader *bufio.Reader) ([]byte, error) {
	for {
		first, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		second, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		opcode := first & 0x0f
		if second&0x80 != 0 {
			return nil, fmt.Errorf("server websocket frame is unexpectedly masked")
		}
		length := uint64(second & 0x7f)
		switch length {
		case 126:
			var value uint16
			if err := binary.Read(reader, binary.BigEndian, &value); err != nil {
				return nil, err
			}
			length = uint64(value)
		case 127:
			if err := binary.Read(reader, binary.BigEndian, &length); err != nil {
				return nil, err
			}
		}
		if length > 16*1024*1024 {
			return nil, fmt.Errorf("manager websocket frame too large: %d", length)
		}
		payload := make([]byte, int(length))
		if _, err := io.ReadFull(reader, payload); err != nil {
			return nil, err
		}
		switch opcode {
		case 0x1:
			return payload, nil
		case 0x8:
			return nil, fmt.Errorf("manager websocket closed: %s", payload)
		case 0x9, 0xA:
			continue
		default:
			continue
		}
	}
}
