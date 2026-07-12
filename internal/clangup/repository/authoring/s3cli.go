package authoring

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type S3CLIStore struct {
	endpoint string
	bucket   string
}

func NewS3CLIStore(endpoint, bucket string) *S3CLIStore {
	return &S3CLIStore{endpoint: endpoint, bucket: bucket}
}

func (store *S3CLIStore) command(arguments ...string) *exec.Cmd {
	base := []string{"s3api"}
	base = append(base, arguments...)
	base = append(base, "--bucket", store.bucket, "--endpoint-url", store.endpoint, "--no-cli-pager")
	command := exec.Command("aws", base...)
	command.Env = append(os.Environ(),
		"AWS_DEFAULT_REGION=auto",
		"AWS_REQUEST_CHECKSUM_CALCULATION=when_required",
		"AWS_RESPONSE_CHECKSUM_VALIDATION=when_required",
	)
	return command
}

func (store *S3CLIStore) Head(key string) (*ObjectInfo, error) {
	command := store.command("head-object", "--key", key, "--output", "json")
	output, err := command.CombinedOutput()
	if err != nil {
		message := string(output)
		if strings.Contains(message, "404") || strings.Contains(message, "Not Found") || strings.Contains(message, "NoSuchKey") {
			return nil, nil
		}
		return nil, fmt.Errorf("aws s3api head-object: %s", strings.TrimSpace(message))
	}
	var value struct {
		ContentLength int64             `json:"ContentLength"`
		ETag          string            `json:"ETag"`
		Metadata      map[string]string `json:"Metadata"`
	}
	if err := json.Unmarshal(output, &value); err != nil {
		return nil, err
	}
	return &ObjectInfo{Size: value.ContentLength, SHA256: value.Metadata["sha256"], ETag: value.ETag}, nil
}

func (store *S3CLIStore) Get(key string) ([]byte, error) {
	directory, err := os.MkdirTemp("", "clangup-catalog-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "catalog.json")
	output, err := store.command("get-object", "--key", key, path).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("aws s3api get-object: %s", strings.TrimSpace(string(output)))
	}
	return os.ReadFile(path)
}

func (store *S3CLIStore) Put(key string, contents []byte, options PutOptions) error {
	directory, err := os.MkdirTemp("", "clangup-catalog-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "catalog.json")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return err
	}
	arguments := []string{
		"put-object", "--key", key, "--body", path,
		"--content-type", options.ContentType,
		"--cache-control", options.CacheControl,
		"--metadata", "sha256=" + options.SHA256,
	}
	if options.IfNoneMatch {
		arguments = append(arguments, "--if-none-match", "*")
	} else if options.IfMatch != "" {
		arguments = append(arguments, "--if-match", options.IfMatch)
	}
	output, err := store.command(arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("aws s3api put-object: %s", strings.TrimSpace(string(output)))
	}
	return nil
}
