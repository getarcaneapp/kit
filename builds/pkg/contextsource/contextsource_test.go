package contextsource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kit.getarcane.app/builds/types"
)

func TestParseGitBuildContextSourceRef(t *testing.T) {
	tests := []struct {
		want       *GitBuildContextSource
		name       string
		raw        string
		wantOptErr bool
	}{
		{
			name: "branch and subdir",
			raw:  "https://github.com/acme/app.git#main:services/api",
			want: &GitBuildContextSource{
				Raw:           "https://github.com/acme/app.git#main:services/api",
				RepositoryURL: "https://github.com/acme/app.git",
				Ref:           "main",
				Subdir:        "services/api",
			},
		},
		{
			name:       "ref that looks like an option",
			raw:        "https://github.com/acme/app.git#--upload-pack=touch",
			wantOptErr: true,
		},
		{
			name:       "option ref with subdir",
			raw:        "https://github.com/acme/app.git# -c:src",
			wantOptErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := ParseGitBuildContextSource(tt.raw)
			assert.True(t, ok)
			if tt.wantOptErr {
				var optErr *types.GitBuildContextRefOptionError
				require.ErrorAs(t, err, &optErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
