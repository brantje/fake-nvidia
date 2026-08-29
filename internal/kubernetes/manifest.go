package kubernetes

import (
	"errors"
	"fmt"
	"strings"
)

// ManifestOptions configures the fake-nvidia Kubernetes installer DaemonSet.
type ManifestOptions struct {
	Namespace   string
	Image       string
	CDIKind     string
	DeviceCount int
	ConfigYAML  []byte
}

// RenderManifest emits a dependency-free Kubernetes manifest containing the
// namespace, profile ConfigMap, and privileged node installer DaemonSet.
func RenderManifest(opts ManifestOptions) ([]byte, error) {
	if strings.TrimSpace(opts.Namespace) == "" {
		opts.Namespace = "fake-nvidia-system"
	}
	if strings.TrimSpace(opts.Image) == "" {
		return nil, errors.New("installer image is required")
	}
	if strings.TrimSpace(opts.CDIKind) == "" {
		opts.CDIKind = DefaultCDIKind
	}
	if opts.DeviceCount <= 0 || opts.DeviceCount > 8 {
		return nil, fmt.Errorf("device count must be between 1 and 8, got %d", opts.DeviceCount)
	}
	if len(strings.TrimSpace(string(opts.ConfigYAML))) == 0 {
		return nil, errors.New("mock NVML config is required")
	}
	if !validDNSLabel(opts.Namespace) {
		return nil, fmt.Errorf("invalid namespace %q", opts.Namespace)
	}
	if !validCDIKind(opts.CDIKind) {
		return nil, fmt.Errorf("invalid CDI kind %q", opts.CDIKind)
	}

	config := indentBlock(string(opts.ConfigYAML), "    ")
	manifest := fmt.Sprintf(`apiVersion: v1
kind: Namespace
metadata:
  name: %[1]s
  labels:
    app.kubernetes.io/part-of: fake-nvidia
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: fake-nvidia-config
  namespace: %[1]s
  labels:
    app.kubernetes.io/name: fake-nvidia
    app.kubernetes.io/component: config
data:
  config.yaml: |
%[5]s---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: fake-nvidia
  namespace: %[1]s
  labels:
    app.kubernetes.io/name: fake-nvidia
    app.kubernetes.io/component: node-installer
spec:
  selector:
    matchLabels:
      app.kubernetes.io/name: fake-nvidia
      app.kubernetes.io/component: node-installer
  template:
    metadata:
      labels:
        app.kubernetes.io/name: fake-nvidia
        app.kubernetes.io/component: node-installer
    spec:
      nodeSelector:
        fake-nvidia.com/enabled: "true"
      terminationGracePeriodSeconds: 15
      tolerations:
        - operator: Exists
      containers:
        - name: installer
          image: %[2]s
          imagePullPolicy: IfNotPresent
          securityContext:
            privileged: true
          args:
            - --host-root=/host/var/lib/fake-nvidia
            - --node-root=/var/lib/fake-nvidia
            - --cdi-dir=/host/var/run/cdi
            - --config=/config/config.yaml
            - --runtime=/opt/fake-nvidia/runtime
            - --device-count=%[3]d
            - --cdi-kind=%[4]s
          env:
            - name: PATH
              value: /opt/fake-nvidia/runtime/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
            - name: LD_LIBRARY_PATH
              value: /opt/fake-nvidia/runtime/lib
            - name: MOCK_NVML_CONFIG
              value: /host/var/lib/fake-nvidia/state/config.yaml
            - name: MOCK_NVML_OVERRIDES
              value: /host/var/lib/fake-nvidia/state/overrides.yaml
          volumeMounts:
            - name: node-var-lib
              mountPath: /host/var/lib
            - name: cdi
              mountPath: /host/var/run/cdi
            - name: config
              mountPath: /config
              readOnly: true
      volumes:
        - name: node-var-lib
          hostPath:
            path: /var/lib
            type: Directory
        - name: cdi
          hostPath:
            path: /var/run/cdi
            type: DirectoryOrCreate
        - name: config
          configMap:
            name: fake-nvidia-config
`, opts.Namespace, opts.Image, opts.DeviceCount, opts.CDIKind, config)
	return []byte(manifest), nil
}

// indentBlock indents a literal YAML block while preserving its line structure.
func indentBlock(value, prefix string) string {
	value = strings.TrimSuffix(value, "\n")
	var b strings.Builder
	for _, line := range strings.Split(value, "\n") {
		b.WriteString(prefix)
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// validDNSLabel validates the subset of DNS labels accepted for namespaces.
func validDNSLabel(value string) bool {
	if len(value) == 0 || len(value) > 63 {
		return false
	}
	for i, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-'
		if !ok || (r == '-' && (i == 0 || i == len(value)-1)) {
			return false
		}
	}
	return true
}
