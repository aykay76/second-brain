package tagging

import (
	"context"
	"testing"
)

func TestParseTags(t *testing.T) {
	tests := []struct {
		name     string
		response string
		max      int
		want     []string
	}{
		{
			name:     "clean lines",
			response: "kubernetes\nmicroservices\ngo\nevent-sourcing\napi-gateway",
			max:      5,
			want:     []string{"kubernetes", "microservices", "go", "event-sourcing", "api-gateway"},
		},
		{
			name:     "numbered list",
			response: "1. kubernetes\n2. microservices\n3. go",
			max:      5,
			want:     []string{"kubernetes", "microservices", "go"},
		},
		{
			name:     "bullet list",
			response: "- kubernetes\n- microservices\n- go",
			max:      5,
			want:     []string{"kubernetes", "microservices", "go"},
		},
		{
			name:     "mixed with empty lines",
			response: "\nkubernetes\n\nmicroservices\n\n",
			max:      5,
			want:     []string{"kubernetes", "microservices"},
		},
		{
			name:     "respects max",
			response: "a\nb\nc\nd\ne\nf\ng",
			max:      3,
			want:     []string{"a", "b", "c"},
		},
		{
			name:     "lowercases tags",
			response: "Kubernetes\nMicroServices\nGo",
			max:      5,
			want:     []string{"kubernetes", "microservices", "go"},
		},
		{
			name:     "skips oversized tags",
			response: "ok\n" + string(make([]byte, 60)) + "\nalso-ok",
			max:      5,
			want:     []string{"ok", "also-ok"},
		},
		{
			name:     "asterisk bullets",
			response: "* kubernetes\n* go\n* docker",
			max:      5,
			want:     []string{"kubernetes", "go", "docker"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTags(tt.response, tt.max)
			if len(got) != len(tt.want) {
				t.Fatalf("parseTags() returned %d tags, want %d: %v", len(got), len(tt.want), got)
			}
			for i, tag := range got {
				if tag != tt.want[i] {
					t.Errorf("tag[%d] = %q, want %q", i, tag, tt.want[i])
				}
			}
		})
	}
}

type mockChat struct {
	response string
	err      error
	calls    int
}

func (m *mockChat) Complete(_ context.Context, _ []interface{ nothing() }) (string, error) {
	// This won't be called; we use the real interface below
	return "", nil
}

func TestServiceConfig(t *testing.T) {
	svc := NewService(nil, nil, Config{})
	if svc.cfg.BatchSize != 20 {
		t.Errorf("default BatchSize = %d, want 20", svc.cfg.BatchSize)
	}
	if svc.cfg.MaxTags != 5 {
		t.Errorf("default MaxTags = %d, want 5", svc.cfg.MaxTags)
	}
}

func TestServiceConfigCustom(t *testing.T) {
	svc := NewService(nil, nil, Config{BatchSize: 50, MaxTags: 3})
	if svc.cfg.BatchSize != 50 {
		t.Errorf("BatchSize = %d, want 50", svc.cfg.BatchSize)
	}
	if svc.cfg.MaxTags != 3 {
		t.Errorf("MaxTags = %d, want 3", svc.cfg.MaxTags)
	}
}
