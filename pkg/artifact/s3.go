package artifact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	DefaultS3Region         = "us-east-1"
	DefaultSourceURLTTL     = 15 * time.Minute
	artifactObjectDirectory = "artifacts"
	sourceObjectDirectory   = "sources"
)

// S3RegistryConfig configures an S3-compatible artifact store. AccessKey and
// SecretKey are runtime values only; config files should use AccessKeyRef and
// SecretKeyRef and resolve them immediately before constructing the registry.
type S3RegistryConfig struct {
	Endpoint     string
	Bucket       string
	Prefix       string
	Region       string
	UsePathStyle bool

	AccessKey    string
	SecretKey    string
	SessionToken string

	AccessKeyRef  string
	SecretKeyRef  string
	ResolveSecret func(context.Context, string) (string, error)
	HTTPClient    *http.Client
}

// S3Registry stores package records and source metadata as JSON objects in an
// S3-compatible bucket. Large source bytes remain in the externally owned
// object named by Source.Path; only metadata is written here.
type S3Registry struct {
	client  *s3.Client
	presign *s3.PresignClient
	cfg     S3RegistryConfig
}

// NewS3Registry constructs an S3-compatible registry using already-resolved
// runtime credentials. Use NewS3RegistryFromRefs for config-file references.
func NewS3Registry(cfg S3RegistryConfig) (*S3Registry, error) {
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.Prefix = strings.Trim(cfg.Prefix, "/")
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 artifact bucket is required")
	}
	if cfg.Region == "" {
		cfg.Region = DefaultS3Region
	}
	awsCfg := aws.Config{Region: cfg.Region, HTTPClient: cfg.HTTPClient}
	if cfg.AccessKey != "" || cfg.SecretKey != "" || cfg.SessionToken != "" {
		awsCfg.Credentials = aws.NewCredentialsCache(credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, cfg.SessionToken))
	}
	client := s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = cfg.UsePathStyle
		if cfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	})
	return &S3Registry{client: client, presign: s3.NewPresignClient(client), cfg: cfg}, nil
}

// NewS3RegistryFromRefs resolves only secret references in memory, then
// constructs the registry. The reference strings themselves are never sent to
// S3 and the resolved values are not retained in S3RegistryConfig.
func NewS3RegistryFromRefs(ctx context.Context, cfg S3RegistryConfig) (*S3Registry, error) {
	resolve := cfg.ResolveSecret
	if resolve == nil {
		resolve = resolveEnvSecret
	}
	if cfg.AccessKeyRef != "" {
		value, err := resolve(ctx, cfg.AccessKeyRef)
		if err != nil {
			return nil, fmt.Errorf("resolve s3 access key: %w", err)
		}
		cfg.AccessKey = value
	}
	if cfg.SecretKeyRef != "" {
		value, err := resolve(ctx, cfg.SecretKeyRef)
		if err != nil {
			return nil, fmt.Errorf("resolve s3 secret key: %w", err)
		}
		cfg.SecretKey = value
	}
	return NewS3Registry(cfg)
}

func resolveEnvSecret(ctx context.Context, ref string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, "env:") || len(ref) == len("env:") {
		return "", fmt.Errorf("secret reference must use env:NAME")
	}
	name := strings.TrimPrefix(ref, "env:")
	value, ok := os.LookupEnv(name)
	if !ok || value == "" {
		return "", fmt.Errorf("environment secret %q is not set", name)
	}
	return value, nil
}

func (r *S3Registry) Publish(ctx context.Context, value Artifact) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if r == nil || r.client == nil {
		return fmt.Errorf("s3 artifact registry is nil")
	}
	if !validArtifactType(value.Type) || value.Ref.Name == "" || value.Ref.Version == "" {
		return fmt.Errorf("artifact type, name, and version are required")
	}
	value.Ref.Type = value.Type
	key := r.artifactKey(value.Ref)
	return r.putImmutable(ctx, key, value, func(data []byte) bool {
		var existing Artifact
		return json.Unmarshal(data, &existing) == nil && sameArtifact(existing, value)
	})
}

func (r *S3Registry) Resolve(ctx context.Context, ref Ref) (Artifact, error) {
	if err := contextErr(ctx); err != nil {
		return Artifact{}, err
	}
	data, err := r.get(ctx, r.artifactKey(ref))
	if err != nil {
		if isS3NotFound(err) {
			return Artifact{}, fmt.Errorf("artifact %q not found", ref.String())
		}
		return Artifact{}, fmt.Errorf("resolve artifact %q: %w", ref.String(), err)
	}
	var value Artifact
	if err := json.Unmarshal(data, &value); err != nil {
		return Artifact{}, fmt.Errorf("decode artifact %q: %w", ref.String(), err)
	}
	if value.Type != ref.Type || value.Ref.Name != ref.Name || value.Ref.Version != ref.Version {
		return Artifact{}, fmt.Errorf("artifact %q has inconsistent identity", ref.String())
	}
	return value, nil
}

func (r *S3Registry) PutSource(ctx context.Context, source Source) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	if r == nil || r.client == nil {
		return fmt.Errorf("s3 artifact registry is nil")
	}
	if strings.TrimSpace(source.Name) == "" || strings.TrimSpace(source.Store) == "" || strings.TrimSpace(source.Path) == "" {
		return fmt.Errorf("source name, store, and path are required")
	}
	if err := ValidateDigest(source.Digest); err != nil {
		return fmt.Errorf("source %q: %w", source.Name, err)
	}
	key := r.sourceKey(source.Name)
	return r.putImmutable(ctx, key, source, func(data []byte) bool {
		var existing Source
		return json.Unmarshal(data, &existing) == nil && sameSource(existing, source)
	})
}

func (r *S3Registry) ResolveSource(ctx context.Context, name string) (Source, error) {
	if err := contextErr(ctx); err != nil {
		return Source{}, err
	}
	data, err := r.get(ctx, r.sourceKey(name))
	if err != nil {
		if isS3NotFound(err) {
			return Source{}, fmt.Errorf("source %q not found", name)
		}
		return Source{}, fmt.Errorf("resolve source %q: %w", name, err)
	}
	var source Source
	if err := json.Unmarshal(data, &source); err != nil {
		return Source{}, fmt.Errorf("decode source %q: %w", name, err)
	}
	if source.Name != strings.TrimSpace(name) {
		return Source{}, fmt.Errorf("source %q has inconsistent identity", name)
	}
	return source, nil
}

// ResolveSourceDescriptor creates an object-specific signed pull descriptor.
// It does not download or copy source bytes through the control plane.
func (r *S3Registry) ResolveSourceDescriptor(ctx context.Context, name string, ttl time.Duration) (SourceDescriptor, error) {
	if ttl <= 0 {
		ttl = DefaultSourceURLTTL
	}
	source, err := r.ResolveSource(ctx, name)
	if err != nil {
		return SourceDescriptor{}, err
	}
	return r.SignSource(ctx, source, ttl)
}

// SignSource creates a signed URL for caller-supplied source metadata. It is
// useful when boxy.yaml is the source-of-truth and no metadata catalog object
// has been written to S3 yet.
func (r *S3Registry) SignSource(ctx context.Context, source Source, ttl time.Duration) (SourceDescriptor, error) {
	if err := contextErr(ctx); err != nil {
		return SourceDescriptor{}, err
	}
	if r == nil || r.presign == nil {
		return SourceDescriptor{}, fmt.Errorf("s3 artifact registry is nil")
	}
	if err := ValidateDigest(source.Digest); err != nil {
		return SourceDescriptor{}, err
	}
	if strings.TrimSpace(source.Path) == "" {
		return SourceDescriptor{}, fmt.Errorf("source path is required")
	}
	if ttl <= 0 {
		ttl = DefaultSourceURLTTL
	}
	presigned, err := r.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(r.cfg.Bucket), Key: aws.String(r.sourceBytesKey(source)),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return SourceDescriptor{}, fmt.Errorf("sign source %q: %w", source.Name, err)
	}
	return SourceDescriptor{
		URL: presigned.URL, Digest: source.Digest, Format: source.Format,
		OS: source.OS, Provider: source.Provider, Metadata: cloneStrings(source.Metadata),
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// SignedSourceURL is a convenience alias for callers that only need the URL.
func (r *S3Registry) SignedSourceURL(ctx context.Context, name string, ttl time.Duration) (string, error) {
	descriptor, err := r.ResolveSourceDescriptor(ctx, name, ttl)
	if err != nil {
		return "", err
	}
	return descriptor.URL, nil
}

func (r *S3Registry) putImmutable(ctx context.Context, key string, value any, equal func([]byte) bool) error {
	if existing, err := r.get(ctx, key); err == nil {
		if equal(existing) {
			return nil
		}
		return fmt.Errorf("object %q is immutable and already published with different content", key)
	} else if !isS3NotFound(err) {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode object %q: %w", key, err)
	}
	_, err = r.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(r.cfg.Bucket), Key: aws.String(key), Body: bytes.NewReader(data),
		IfNoneMatch: aws.String("*"),
	})
	if err != nil {
		// A concurrent publisher may have won the create race. Re-read and
		// accept only identical bytes, preserving immutability.
		if existing, readErr := r.get(ctx, key); readErr == nil && equal(existing) {
			return nil
		}
		return fmt.Errorf("publish object %q: %w", key, err)
	}
	return nil
}

func (r *S3Registry) get(ctx context.Context, key string) ([]byte, error) {
	response, err := r.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.cfg.Bucket), Key: aws.String(key)})
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if err := response.Body.Close(); err != nil {
		return nil, err
	}
	return data, nil
}

func (r *S3Registry) artifactKey(ref Ref) string {
	return r.objectKey(artifactObjectDirectory, string(ref.Type), ref.Name, ref.Version+".json")
}

func (r *S3Registry) sourceKey(name string) string {
	return r.objectKey(sourceObjectDirectory, strings.TrimSpace(name)+".json")
}

func (r *S3Registry) sourceBytesKey(source Source) string {
	// Source.Path is relative to the configured store prefix, matching
	// published package/object metadata. The developer still owns the bytes
	// and uploads them with storage tooling.
	return r.objectKey(source.Path)
}

func (r *S3Registry) objectKey(parts ...string) string {
	all := append([]string{r.cfg.Prefix}, parts...)
	return path.Join(all...)
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var responseErr interface{ HTTPStatusCode() int }
	if errors.As(err, &responseErr) {
		return responseErr.HTTPStatusCode() == http.StatusNotFound
	}
	return strings.Contains(strings.ToLower(err.Error()), "nosuchkey") || strings.Contains(strings.ToLower(err.Error()), "status code: 404")
}
