package collection

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRenditionConfigCache(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.October, 27, 12, 0, 0, 0, time.UTC)
	want := renditionConfig{RecipientUID: "merchant", Bank: "bank", Account: "account", Policy: RenditionPolicy{Mode: "APPLY"}}
	tests := []struct {
		name  string
		calls []struct {
			parameter string
			at        time.Time
		}
		loadErrorAt int
		wantLoads   int
		wantError   bool
	}{
		{
			name: "reuse within ttl",
			calls: []struct {
				parameter string
				at        time.Time
			}{{"/config", base}, {"/config", base.Add(4 * time.Minute)}},
			wantLoads: 1,
		},
		{
			name: "refresh at expiration",
			calls: []struct {
				parameter string
				at        time.Time
			}{{"/config", base}, {"/config", base.Add(renditionConfigCacheTTL)}},
			wantLoads: 2,
		},
		{
			name: "separate parameter names",
			calls: []struct {
				parameter string
				at        time.Time
			}{{"/prd/config", base}, {"/dev/config", base.Add(time.Minute)}},
			wantLoads: 2,
		},
		{
			name: "do not serve expired value after load error",
			calls: []struct {
				parameter string
				at        time.Time
			}{{"/config", base}, {"/config", base.Add(renditionConfigCacheTTL)}},
			loadErrorAt: 2,
			wantLoads:   2,
			wantError:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cache renditionConfigCache
			loads := 0
			var gotErr error
			for _, call := range tt.calls {
				got, err := cache.get(context.Background(), call.parameter, call.at, func(context.Context) (renditionConfig, error) {
					loads++
					if loads == tt.loadErrorAt {
						return renditionConfig{}, errors.New("ssm unavailable")
					}
					return want, nil
				})
				if err != nil {
					gotErr = err
					continue
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("cache.get() = %#v, want %#v", got, want)
				}
			}
			if loads != tt.wantLoads {
				t.Fatalf("loads = %d, want %d", loads, tt.wantLoads)
			}
			if (gotErr != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %v", gotErr, tt.wantError)
			}
		})
	}
}
