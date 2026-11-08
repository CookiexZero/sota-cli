package account

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func ReadKey(keyFile, envFile string) (string, error) {
	var key string
	if keyFile != "" {
		data, err := readSmall(keyFile)
		if err != nil {
			return "", err
		}
		key = strings.TrimSpace(string(data))
	} else if envFile != "" {
		data, err := readSmall(envFile)
		if err != nil {
			return "", err
		}
		for _, line := range strings.Split(string(data), "\n") {
			name, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if ok && name == "SOTA_SUB_KEY" {
				key = strings.Trim(strings.TrimSpace(value), "\"'")
			}
		}
	} else {
		key = strings.TrimSpace(os.Getenv("SOTA_SUB_KEY"))
	}
	if !keyPattern.MatchString(key) {
		return "", errors.New("SOTA_SUB_KEY must contain a UUID; use --key-file, --env-file, or the environment")
	}
	return key, nil
}

func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open credential file")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return nil, errors.New("credential file is unreadable or exceeds 64 KiB")
	}
	return data, nil
}

func SaveKey(dir, key string) error {
	if !keyPattern.MatchString(key) {
		return errors.New("invalid access key")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errors.New("cannot create credential directory")
	}
	f, err := os.CreateTemp(dir, ".access-key-*")
	if err != nil {
		return errors.New("cannot create credential file")
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(key + "\n"); err != nil {
		f.Close()
		return errors.New("cannot write credential file")
	}
	if err = f.Close(); err != nil {
		return errors.New("cannot close credential file")
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, "access-key")); err != nil {
		return errors.New("cannot save credential file")
	}
	return nil
}

func StoredKey(dir string) (string, error) { return ReadKey(filepath.Join(dir, "access-key"), "") }
