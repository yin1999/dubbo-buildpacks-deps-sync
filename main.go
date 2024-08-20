package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/hashicorp/go-envparse"
	"github.com/pelletier/go-toml/v2"
	"github.com/schollz/progressbar/v3"
)

type doc struct {
	Metadata metadata `toml:"metadata"`
}

type metadata struct {
	Dependencies []dependency `toml:"dependencies"`
}

type dependency struct {
	ID      string `toml:"id"`
	Sha256  string `toml:"sha256"`
	Uri     string `toml:"uri"`
	Version string `toml:"version"`
	url     *url.URL
}

func (d *dependency) getObjectKey() (string, error) {
	if d.url != nil {
		return d.url.Path, nil
	}
	var err error
	d.url, err = url.Parse(d.Uri)
	if err != nil {
		return "", err
	}
	return d.url.Path, nil
}

func resolvePath(p string) (string, error) {
	// replace '+' with ' '
	path, err := url.QueryUnescape(p)
	if err != nil {
		return "", err
	}
	path = strings.TrimPrefix(path, "/")
	return path, nil
}

const (
	sha256MetadataKey = "sha256"
)

func main() {
	var (
		url, token                   string
		region, bucket               string
		accessKeyID, accessKeySecret string
		envFile                      string
	)
	flag.StringVar(&url, "url", "", "buildpack.toml URL")
	flag.StringVar(&token, "token", token, "GitHub token")
	flag.StringVar(&region, "region", region, "region")
	flag.StringVar(&bucket, "bucket", bucket, "bucket")
	flag.StringVar(&accessKeyID, "ak", accessKeyID, "access key id")
	flag.StringVar(&accessKeySecret, "sk", accessKeySecret, "access key secret")
	flag.StringVar(&envFile, "env", ".env", "env file")
	flag.Parse()

	importEnv(envFile)

	err := loadFromEnvAndCheck([]*string{
		&url, &region, &bucket,
		&accessKeyID, &accessKeySecret,
	}, []string{
		"URL", "REGION", "BUCKET",
		"ACCESS_KEY", "ACCESS_KEY_SECRET",
	})
	if err != nil {
		log.Fatalf("checking failed: %v", err)
	}

	token = os.Getenv("GITHUB_TOKEN")

	deps, err := requiredFiles(url, token)
	if err != nil {
		log.Fatalf("failed to get required files: %v", err)
	}

	cfg := oss.LoadDefaultConfig().
		WithCredentialsProvider(credentials.CredentialsProviderFunc(
			func(ctx context.Context) (credentials.Credentials, error) {
				return credentials.Credentials{
					AccessKeyID:     accessKeyID,
					AccessKeySecret: accessKeySecret,
				}, nil
			}),
		).WithRegion(region)

	client := oss.NewClient(cfg)
	deps, err = filterFiles(client, bucket, deps)
	if err != nil {
		log.Fatalf("failed to filter files: %v", err)
	}
	if len(deps) == 0 {
		log.Println("All files are up to date")
		return
	}
	err = transferFiles(client, bucket, token, deps)
	if err != nil {
		log.Fatalf("failed to transfer files: %v", err)
	}
	log.Printf("Successfully transferred %d files", len(deps))
}

func loadFromEnvAndCheck(keys []*string, envs []string) error {
	for i, key := range keys {
		if *key == "" {
			*key = os.Getenv(envs[i])
		}
		if *key == "" {
			return fmt.Errorf("%s is required", envs[i])
		}
	}
	return nil
}

func importEnv(envFile string) {
	file, err := os.Open(envFile)
	if err != nil {
		log.Printf("skip loading env file: %v", err)
		return
	}
	defer file.Close()
	env, err := envparse.Parse(file)
	if err != nil {
		log.Fatalf("failed to load env file: %v", err)
	}
	for k, v := range env {
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}

func requiredFiles(url string, ghToken string) ([]dependency, error) {
	req, err := http.NewRequest(http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	setGhHeader(&req.Header, ghToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var d doc
	if err = toml.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, err
	}
	return d.Metadata.Dependencies, nil
}

func filterFiles(c *oss.Client, bucket string, deps []dependency) ([]dependency, error) {
	filtered := make([]dependency, 0, len(deps))
	for _, dep := range deps {
		path, err := dep.getObjectKey()
		if err != nil {
			return nil, err
		}
		path, err = resolvePath(path)
		if err != nil {
			return nil, err
		}
		res, err := c.HeadObject(context.Background(), &oss.HeadObjectRequest{
			Bucket: oss.Ptr(bucket),
			Key:    oss.Ptr(path),
		})
		if err != nil && !strings.Contains(err.Error(), "NoSuchKey") {
			return nil, fmt.Errorf("failed to get object meta: %w", err)
		}
		if err != nil || res.Metadata[sha256MetadataKey] != dep.Sha256 {
			filtered = append(filtered, dep)
		}
	}
	return filtered, nil
}

func setGhHeader(h *http.Header, ghToken string) {
	if ghToken != "" {
		h.Set("Authorization", fmt.Sprintf("Bearer %s", ghToken))
	}
}

func transferFiles(c *oss.Client, bucket, ghToken string, deps []dependency) error {
	for _, dep := range deps {
		path, err := dep.getObjectKey()
		if err != nil {
			return err
		}
		path, err = resolvePath(path)
		if err != nil {
			return err
		}
		req, err := http.NewRequest(http.MethodGet, dep.Uri, http.NoBody)
		if err != nil {
			return err
		}
		setGhHeader(&req.Header, ghToken)

		log.Printf("Downloading %q", dep.Uri)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}

		log.Printf("Transferring %q to %q", dep.Uri, path)
		bar := progressbar.DefaultBytes(
			resp.ContentLength,
			"transferring",
		)
		reader := progressbar.NewReader(resp.Body, bar)

		_, err = c.PutObject(context.Background(), &oss.PutObjectRequest{
			Bucket: oss.Ptr(bucket),
			Key:    oss.Ptr(path),
			Body:   &reader,
			Metadata: map[string]string{
				sha256MetadataKey: dep.Sha256,
			},
		})
		reader.Close()
		if err != nil {
			return fmt.Errorf("failed to transfer %q: %w", dep.Uri, err)
		}
		log.Printf("Successfully transferred %q", dep.Uri)
	}
	return nil
}
