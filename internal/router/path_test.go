package router

import "testing"

func TestParsePath(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantRegion string
		wantPath   string
		wantBucket string
		wantErr    bool
	}{
		{
			name:       "valid path",
			input:      "/na1/lol/summoner/v4/summoners/by-name/test",
			wantRegion: "na1",
			wantPath:   "/lol/summoner/v4/summoners/by-name/test",
			wantBucket: "na1:lol/summoner/v4/summoners/by-name/test",
		},
		{
			name:       "region is normalized to lower-case",
			input:      "/EUW1/lol/status/v4/platform-data",
			wantRegion: "euw1",
			wantPath:   "/lol/status/v4/platform-data",
			wantBucket: "euw1:lol/status/v4/platform-data",
		},
		{
			name:    "missing route part",
			input:   "/na1",
			wantErr: true,
		},
		{
			name:    "invalid region characters",
			input:   "/na!1/lol/status/v4/platform-data",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePath(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Region != tt.wantRegion {
				t.Fatalf("expected region %q, got %q", tt.wantRegion, info.Region)
			}
			if info.UpstreamPath != tt.wantPath {
				t.Fatalf("expected path %q, got %q", tt.wantPath, info.UpstreamPath)
			}
			if info.Bucket != tt.wantBucket {
				t.Fatalf("expected bucket %q, got %q", tt.wantBucket, info.Bucket)
			}
		})
	}
}
