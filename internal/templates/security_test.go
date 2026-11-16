package templates

import (
	"strings"
	"testing"
)

func TestApplyDefaultsRejectsUnsafePods(t *testing.T) {
	cases := map[string]string{
		"privileged": `
  containers:
    - name: workspace
      securityContext:
        privileged: true
`,
		"hostNetwork": `
  hostNetwork: true
`,
		"hostPID": `
  hostPID: true
`,
		"hostIPC": `
  hostIPC: true
`,
		"serviceAccount": `
  serviceAccountName: default
`,
		"automountToken": `
  automountServiceAccountToken: true
`,
		"priorityClass": `
  priorityClassName: system-node-critical
`,
		"sysctls": `
  securityContext:
    sysctls:
      - name: kernel.shm_rmid_forced
        value: "1"
`,
		"unconfinedSeccomp": `
  securityContext:
    seccompProfile:
      type: Unconfined
`,
		"hostPath": `
  volumes:
    - name: host
      hostPath:
        path: /
`,
		"secretVolume": `
  volumes:
    - name: secret
      secret:
        secretName: other-workspace
`,
		"projectedVolume": `
  volumes:
    - name: projected
      projected:
        sources:
          - serviceAccountToken:
              path: token
`,
		"crossTenantPVC": `
  volumes:
    - name: other
      persistentVolumeClaim:
        claimName: poddy-other
`,
		"hostPort": `
  containers:
    - name: workspace
      ports:
        - containerPort: 8080
          hostPort: 8080
`,
		"secretEnv": `
  containers:
    - name: workspace
      env:
        - name: LEAK
          valueFrom:
            secretKeyRef:
              name: other-workspace
              key: token
`,
		"secretEnvFrom": `
  containers:
    - name: workspace
      envFrom:
        - secretRef:
            name: other-workspace
`,
		"privilegedCapability": `
  containers:
    - name: workspace
      securityContext:
        capabilities:
          add:
            - SYS_ADMIN
`,
		"procMount": `
  containers:
    - name: workspace
      securityContext:
        procMount: Unmasked
`,
		"initContainerPrivileged": `
  initContainers:
    - name: setup
      image: busybox
      securityContext:
        privileged: true
`,
	}

	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			raw := "apiVersion: v1\nkind: Pod\nspec:\n" + spec
			if _, err := ApplyDefaults([]byte(raw), testData()); err == nil {
				t.Fatalf("expected unsafe pod to be rejected:\n%s", raw)
			} else if !strings.Contains(err.Error(), "unsafe pod template") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestApplyDefaultsAllowsWorkspaceCapabilityAndOwnPVC(t *testing.T) {
	raw := []byte(`
apiVersion: v1
kind: Pod
spec:
  containers:
    - name: workspace
      securityContext:
        capabilities:
          add:
            - SYS_RESOURCE
      volumeMounts:
        - name: data
          mountPath: /data
  volumes:
    - name: data
      persistentVolumeClaim:
        claimName: poddy-app
`)
	pod, err := ApplyDefaults(raw, testData())
	if err != nil {
		t.Fatal(err)
	}
	if len(pod.Spec.Volumes) != 2 {
		t.Fatalf("volumes = %+v", pod.Spec.Volumes)
	}
}
