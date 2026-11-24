package netmon

import (
	"context"
	"fmt"
	"log"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const workspaceLabel = "poddy.io/workspace"

type PodIndex struct {
	client    kubernetes.Interface
	namespace string
	node      string
	mu        sync.RWMutex
	pods      map[string]PodInfo
	synced    bool
}

func NewPodIndex(client kubernetes.Interface, namespace, node string) *PodIndex {
	return &PodIndex{
		client:    client,
		namespace: namespace,
		node:      node,
		pods:      make(map[string]PodInfo),
	}
}

func (i *PodIndex) Refresh(ctx context.Context) error {
	if i == nil || i.client == nil {
		return fmt.Errorf("pod index: kubernetes client is not configured")
	}
	list, err := i.client.CoreV1().Pods(i.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	next := make(map[string]PodInfo)
	for idx := range list.Items {
		pod := &list.Items[idx]
		workspace := strings.TrimSpace(pod.Labels[workspaceLabel])
		if workspace == "" {
			continue
		}
		if i.node != "" && pod.Spec.NodeName != i.node {
			continue
		}
		info := PodInfo{
			Workspace: workspace,
			Namespace: pod.Namespace,
			Node:      pod.Spec.NodeName,
			UID:       string(pod.UID),
		}
		if pod.Status.PodIP != "" {
			info.IPs = append(info.IPs, pod.Status.PodIP)
		}
		for _, ip := range pod.Status.PodIPs {
			if ip.IP != "" && ip.IP != pod.Status.PodIP {
				info.IPs = append(info.IPs, ip.IP)
			}
		}
		for _, ip := range info.IPs {
			next[ip] = info
		}
	}
	i.mu.Lock()
	i.pods = next
	i.synced = true
	i.mu.Unlock()
	return nil
}

func (i *PodIndex) Lookup(ip string) (PodInfo, bool) {
	if i == nil || ip == "" {
		return PodInfo{}, false
	}
	i.mu.RLock()
	info, ok := i.pods[ip]
	i.mu.RUnlock()
	return info, ok
}

func (i *PodIndex) Synced() bool {
	if i == nil {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.synced
}

func (i *PodIndex) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	if err := i.Refresh(ctx); err != nil {
		log.Printf("netmon pod index: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := i.Refresh(ctx); err != nil {
				log.Printf("netmon pod index: %v", err)
			}
		}
	}
}

type MountIndex struct {
	client    kubernetes.Interface
	namespace string
	node      string
	resolver  *net.Resolver
	mu        sync.RWMutex
	mounts    []MountInfo
	byServer  map[string][]MountInfo
	synced    bool
}

func NewMountIndex(client kubernetes.Interface, namespace, node string) *MountIndex {
	return &MountIndex{
		client:    client,
		namespace: namespace,
		node:      node,
		resolver:  net.DefaultResolver,
		byServer:  make(map[string][]MountInfo),
	}
}

func (i *MountIndex) Refresh(ctx context.Context) error {
	if i == nil || i.client == nil {
		return fmt.Errorf("mount index: kubernetes client is not configured")
	}
	pods, err := i.client.CoreV1().Pods(i.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	claims, err := i.client.CoreV1().PersistentVolumeClaims(i.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	volumes, err := i.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	claimByName := make(map[string]*corev1.PersistentVolumeClaim, len(claims.Items))
	for idx := range claims.Items {
		claim := &claims.Items[idx]
		claimByName[claim.Name] = claim
	}
	volumeByName := make(map[string]*corev1.PersistentVolume, len(volumes.Items))
	for idx := range volumes.Items {
		volume := &volumes.Items[idx]
		volumeByName[volume.Name] = volume
	}

	seen := make(map[string]struct{})
	next := make([]MountInfo, 0)
	for idx := range pods.Items {
		pod := &pods.Items[idx]
		workspace := strings.TrimSpace(pod.Labels[workspaceLabel])
		if workspace == "" {
			continue
		}
		if i.node != "" && pod.Spec.NodeName != i.node {
			continue
		}
		for _, volume := range pod.Spec.Volumes {
			if volume.PersistentVolumeClaim == nil {
				continue
			}
			claim := claimByName[volume.PersistentVolumeClaim.ClaimName]
			if claim == nil || claim.Spec.VolumeName == "" {
				continue
			}
			pv := volumeByName[claim.Spec.VolumeName]
			if pv == nil {
				continue
			}
			info, ok := MountInfoFromPV(pv)
			if !ok {
				continue
			}
			info.Workspace = workspace
			info.Namespace = pod.Namespace
			info.PVC = claim.Name
			info.Volume = pv.Name
			info.AccessMode = accessModes(claim.Spec.AccessModes)
			key := info.Workspace + "|" + info.PVC + "|" + info.Volume
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			next = append(next, info)
		}
	}
	sort.Slice(next, func(a, b int) bool {
		if next[a].Workspace != next[b].Workspace {
			return next[a].Workspace < next[b].Workspace
		}
		return next[a].PVC < next[b].PVC
	})

	byServer := make(map[string][]MountInfo)
	for idx := range next {
		info := next[idx]
		ips := append([]string(nil), info.ServerIPs...)
		if len(ips) == 0 && info.Server != "" {
			ips = resolveHost(ctx, i.resolver, info.Server)
			next[idx].ServerIPs = ips
		}
		for _, ip := range ips {
			byServer[ip] = append(byServer[ip], next[idx])
		}
	}

	i.mu.Lock()
	i.mounts = next
	i.byServer = byServer
	i.synced = true
	i.mu.Unlock()
	return nil
}

func (i *MountIndex) Mounts() []MountInfo {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return append([]MountInfo(nil), i.mounts...)
}

func (i *MountIndex) WorkspacesForServer(ip string) []string {
	if i == nil || ip == "" {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, info := range i.byServer[ip] {
		if info.Workspace == "" {
			continue
		}
		if _, ok := seen[info.Workspace]; ok {
			continue
		}
		seen[info.Workspace] = struct{}{}
		out = append(out, info.Workspace)
	}
	sort.Strings(out)
	return out
}

func (i *MountIndex) HostForServer(ip string) (string, bool) {
	if i == nil || ip == "" {
		return "", false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	for _, info := range i.byServer[ip] {
		if info.Server != "" {
			return info.Server, true
		}
	}
	return "", false
}

func (i *MountIndex) Synced() bool {
	if i == nil {
		return false
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.synced
}

func (i *MountIndex) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	if err := i.Refresh(ctx); err != nil {
		log.Printf("netmon mount index: %v", err)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := i.Refresh(ctx); err != nil {
				log.Printf("netmon mount index: %v", err)
			}
		}
	}
}

func MountInfoFromPV(pv *corev1.PersistentVolume) (MountInfo, bool) {
	if pv == nil {
		return MountInfo{}, false
	}
	info := MountInfo{}
	if pv.Spec.NFS != nil {
		info.Server = pv.Spec.NFS.Server
		info.Path = pv.Spec.NFS.Path
		info.FSType = "nfs"
		info.Service = ServiceNFS
		if isEFSHost(info.Server) {
			info.Service = ServiceEFS
			info.FSType = "nfs4"
		}
		return info, true
	}
	if pv.Spec.CSI != nil {
		csi := pv.Spec.CSI
		attrs := csi.VolumeAttributes
		info.Provisioner = csi.Driver
		switch {
		case csi.Driver == "efs.csi.aws.com":
			info.Service = ServiceEFS
			info.FSType = "nfs4"
			info.Server = firstNonEmpty(attrs["server"], attrs["dnsName"], efsServerFromHandle(csi.VolumeHandle))
			info.Path = firstNonEmpty(attrs["path"], "/")
		case strings.Contains(strings.ToLower(csi.Driver), "s3"):
			info.Service = ServiceS3
			info.FSType = "fuse"
			info.Bucket = firstNonEmpty(attrs["bucketName"], attrs["bucket"], csi.VolumeHandle)
			info.Path = attrs["mountPath"]
		case strings.Contains(strings.ToLower(csi.Driver), "nfs"):
			info.Service = ServiceNFS
			info.FSType = "nfs"
			info.Server = firstNonEmpty(attrs["server"], attrs["host"])
			info.Path = firstNonEmpty(attrs["share"], attrs["path"], "/")
		default:
			info.Service = ServiceExternal
			info.FSType = attrs["fstype"]
			info.Server = firstNonEmpty(attrs["server"], attrs["host"])
			info.Path = firstNonEmpty(attrs["path"], attrs["share"])
		}
		if info.Server == "" && info.Bucket == "" {
			return MountInfo{}, false
		}
		return info, true
	}
	return MountInfo{}, false
}

func efsServerFromHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	if idx := strings.Index(handle, "::"); idx > 0 {
		handle = handle[:idx]
	}
	if strings.HasPrefix(handle, "fs-") {
		return handle + ".efs.amazonaws.com"
	}
	return ""
}

func accessModes(modes []corev1.PersistentVolumeAccessMode) string {
	out := make([]string, 0, len(modes))
	for _, mode := range modes {
		out = append(out, string(mode))
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func resolveHost(ctx context.Context, resolver *net.Resolver, host string) []string {
	lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	ips, err := resolver.LookupHost(lookupCtx, host)
	if err != nil {
		return nil
	}
	sort.Strings(ips)
	return ips
}
