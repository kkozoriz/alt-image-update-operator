package image

import (
	"strings"
	"testing"
)

func TestBuildReferenceReplacesExplicitTag(t *testing.T) {
	got, err := BuildReference("localhost:5001/team/demo/app:v1.2.3", "g7-abcdef0123456789")
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}

	want := "localhost:5001/team/demo/app:g7-abcdef0123456789"
	if got != want {
		t.Fatalf("BuildReference() = %q, want %q", got, want)
	}
}

func TestBuildReferenceAddsTagWhenMissing(t *testing.T) {
	got, err := BuildReference("registry.example.com/security/alt/demo-app", "g1-0123456789abcdef")
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}

	want := "registry.example.com/security/alt/demo-app:g1-0123456789abcdef"
	if got != want {
		t.Fatalf("BuildReference() = %q, want %q", got, want)
	}
}

func TestBuildReferencePreservesNestedRepositoryWithoutRegistry(t *testing.T) {
	got, err := BuildReference("alt/security/demo-app:stable", "g2-fedcba9876543210")
	if err != nil {
		t.Fatalf("BuildReference returned error: %v", err)
	}

	want := "alt/security/demo-app:g2-fedcba9876543210"
	if got != want {
		t.Fatalf("BuildReference() = %q, want %q", got, want)
	}
}

func TestRepositoryDropsExistingTag(t *testing.T) {
	got, err := Repository("localhost:5001/team/demo/app:v1.2.3")
	if err != nil {
		t.Fatalf("Repository returned error: %v", err)
	}

	want := "localhost:5001/team/demo/app"
	if got != want {
		t.Fatalf("Repository() = %q, want %q", got, want)
	}
}

func TestBuildReferenceInvalidOutputImage(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)

	tests := []struct {
		name        string
		outputImage string
		wantErr     string
	}{
		{
			name:        "empty",
			outputImage: "",
			wantErr:     "value is empty",
		},
		{
			name:        "blank",
			outputImage: "  ",
			wantErr:     "value is empty",
		},
		{
			name:        "space in reference",
			outputImage: "registry.example.com/team/demo app",
			wantErr:     "invalid output image reference",
		},
		{
			name:        "uppercase repository",
			outputImage: "registry.example.com/Team/demo-app",
			wantErr:     "repository name must be lowercase",
		},
		{
			name:        "digest destination",
			outputImage: "registry.example.com/team/demo-app@" + digest,
			wantErr:     "digest references are not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildReference(tt.outputImage, "g1-0123456789abcdef")
			if err == nil {
				t.Fatal("BuildReference returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("BuildReference error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildReferenceInvalidBuildID(t *testing.T) {
	tests := []struct {
		name    string
		buildID string
		wantErr string
	}{
		{
			name:    "empty",
			buildID: "",
			wantErr: "value is empty",
		},
		{
			name:    "blank",
			buildID: "  ",
			wantErr: "value is empty",
		},
		{
			name:    "space",
			buildID: "g1 bad",
			wantErr: "invalid tag format",
		},
		{
			name:    "starts with dash",
			buildID: "-g1",
			wantErr: "invalid tag format",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := BuildReference("registry.example.com/team/demo-app", tt.buildID)
			if err == nil {
				t.Fatal("BuildReference returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("BuildReference error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
