// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

const ConfigBundleIndexFileName = "index.yaml"

var ErrBundleChecksumMismatch = errors.New("bundle checksum mismatch")

// ConfigBundleIndex describes where to find sharded filter config parts and how to validate them.
type ConfigBundleIndex struct {
	Version   string             `json:"version" yaml:"version"`
	UUID      string             `json:"uuid" yaml:"uuid"`
	Checksum  string             `json:"checksum" yaml:"checksum"`
	Parts     []ConfigBundlePart `json:"parts" yaml:"parts"`
	CreatedAt *time.Time         `json:"createdAt,omitempty" yaml:"createdAt,omitempty"`
}

type ConfigBundlePart struct {
	Name      string `json:"name" yaml:"name"`
	Path      string `json:"path" yaml:"path"`
	SizeBytes int    `json:"sizeBytes,omitempty" yaml:"sizeBytes,omitempty"`
}

func ConfigBundlePartPath(idx int) string {
	return fmt.Sprintf("parts/%03d", idx)
}

func MarshalConfigBundleIndex(index *ConfigBundleIndex) ([]byte, error) {
	return yaml.Marshal(index)
}

func UnmarshalConfigBundleIndex(raw []byte) (*ConfigBundleIndex, error) {
	var index ConfigBundleIndex
	if err := yaml.Unmarshal(raw, &index); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config bundle index: %w", err)
	}
	if index.Checksum == "" {
		return nil, fmt.Errorf("invalid config bundle index: empty checksum value")
	}
	if len(index.Parts) == 0 {
		return nil, fmt.Errorf("invalid config bundle index: empty parts")
	}
	return &index, nil
}

func ConfigBundleChecksum(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// ReassembleBundleConfig rebuilds a full filter config from sharded parts and verifies integrity.
func ReassembleBundleConfig(index *ConfigBundleIndex, readPart func(part ConfigBundlePart) ([]byte, error)) (*Config, error) {
	var payload []byte
	for i := 0; i < len(index.Parts); i++ {
		p := ConfigBundlePart{
			Name: index.Parts[i].Name,
			Path: index.Parts[i].Path,
		}
		b, err := readPart(p)
		if err != nil {
			return nil, fmt.Errorf("failed to read bundle part %q: %w", p.Path, err)
		}
		payload = append(payload, b...)
	}

	expected := strings.ToLower(index.Checksum)
	actual := ConfigBundleChecksum(payload)
	if actual != expected {
		return nil, fmt.Errorf("%w: expected %s got %s", ErrBundleChecksumMismatch, expected, actual)
	}

	var cfg Config
	if err := yaml.Unmarshal(payload, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal bundled config: %w", err)
	}
	return &cfg, nil
}
