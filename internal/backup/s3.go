// Package backup stores workspace volume archives in S3-compatible object
// storage.
package backup

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// Config describes an S3-compatible backup target.
type Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	Prefix    string
	UseSSL    bool
}

// Object describes a stored volume archive.
type Object struct {
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	LastModified time.Time `json:"lastModified"`
}

// Store reads and writes volume archives in an S3 bucket.
type Store struct {
	client *minio.Client
	bucket string
	prefix string
}

// New creates a backup store. It returns nil when the configuration is empty.
func New(cfg Config) (*Store, error) {
	endpoint := strings.TrimSpace(cfg.Endpoint)
	bucket := strings.TrimSpace(cfg.Bucket)
	accessKey := strings.TrimSpace(cfg.AccessKey)
	secretKey := strings.TrimSpace(cfg.SecretKey)
	if endpoint == "" && bucket == "" && accessKey == "" && secretKey == "" {
		return nil, nil
	}
	if endpoint == "" || bucket == "" || accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("backup: endpoint, bucket, access key, and secret key are required")
	}

	useSSL := cfg.UseSSL
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" {
		useSSL = parsed.Scheme == "https"
		endpoint = parsed.Host
	}
	endpoint = strings.TrimSuffix(strings.TrimPrefix(endpoint, "http://"), "/")
	endpoint = strings.TrimSuffix(strings.TrimPrefix(endpoint, "https://"), "/")

	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
		Region: strings.TrimSpace(cfg.Region),
	})
	if err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}

	prefix := strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	if prefix == "" {
		prefix = "poddy-backups"
	}
	return &Store{client: client, bucket: bucket, prefix: prefix}, nil
}

// Enabled reports whether the store is configured.
func (s *Store) Enabled() bool { return s != nil && s.client != nil }

// VolumePrefix returns the object prefix that contains a volume's archives.
func (s *Store) VolumePrefix(volume string) string {
	return path.Join(s.prefix, strings.TrimSpace(volume)) + "/"
}

// Key returns the object key for a volume backup at the given time.
func (s *Store) Key(volume string, at time.Time) string {
	name := strings.TrimSpace(volume)
	if name == "" {
		name = "volume"
	}
	return path.Join(s.prefix, name, at.UTC().Format("20060102T150405Z")+".tar.gz")
}

// Put uploads an archive. Use size -1 when the size is not known in advance.
func (s *Store) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	if !s.Enabled() {
		return fmt.Errorf("backup storage is not configured")
	}
	_, err := s.client.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("backup: upload %s: %w", key, err)
	}
	return nil
}

// List returns archives for a volume, newest first.
func (s *Store) List(ctx context.Context, volume string) ([]Object, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("backup storage is not configured")
	}
	prefix := s.VolumePrefix(volume)
	objects := make([]Object, 0)
	for info := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if info.Err != nil {
			return nil, fmt.Errorf("backup: list %s: %w", prefix, info.Err)
		}
		if !strings.HasSuffix(info.Key, ".tar.gz") {
			continue
		}
		objects = append(objects, Object{
			Key:          info.Key,
			Name:         path.Base(info.Key),
			Size:         info.Size,
			LastModified: info.LastModified,
		})
	}
	for i := 0; i < len(objects); i++ {
		for j := i + 1; j < len(objects); j++ {
			if objects[j].LastModified.After(objects[i].LastModified) {
				objects[i], objects[j] = objects[j], objects[i]
			}
		}
	}
	return objects, nil
}

// Get opens an archive for reading.
func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("backup storage is not configured")
	}
	object, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("backup: download %s: %w", key, err)
	}
	if _, err := object.Stat(); err != nil {
		object.Close()
		return nil, fmt.Errorf("backup: download %s: %w", key, err)
	}
	return object, nil
}

// Delete removes an archive.
func (s *Store) Delete(ctx context.Context, key string) error {
	if !s.Enabled() {
		return fmt.Errorf("backup storage is not configured")
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("backup: delete %s: %w", key, err)
	}
	return nil
}

// ValidKey reports whether key belongs to the store prefix and a volume.
func (s *Store) ValidKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return false
	}
	return strings.HasPrefix(key, s.prefix+"/") && strings.HasSuffix(key, ".tar.gz")
}
