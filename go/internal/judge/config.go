package judge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"time"

	"github.com/Quentin-M/harness/internal/fsx"
)

// syncConfig points the plugin at the local server and the gate battery. The harness owns exactly
// three keys: judgeBackend, battery and hardBlocks (the battery's rule is the only policy, so the
// plugin's built-in hard blocks stay off). Every launch restores them, so a /classifier reset or a
// stale port cannot leave the gate on the remote judge. Every other key (enabled, timeoutMs, jevPolicy, reviewerModel...) belongs to the
// user's /classifier edits and survives untouched.
func (l *Local) syncConfig(model string) error {
	// concurrent launches share fsx.Write's temp name; the lock keeps one writer at a time
	lock, err := lockUntil(l.dir("config.lock"), l.Now().Add(5*time.Second), l.Now)
	if err != nil {
		return err
	}
	defer lock.Close()
	p := ConfigPath(l.State)
	cfg := map[string]any{}
	data, err := os.ReadFile(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	default:
		if err := json.Unmarshal(data, &cfg); err != nil || cfg == nil {
			return fmt.Errorf("%s is not a JSON object; fix or delete it", p)
		}
	}
	want := map[string]any{
		"judgeBackend": map[string]any{"kind": "endpoint", "baseUrl": l.baseURL(), "model": model, "apiKeyEnv": KeyEnv},
		"battery":      l.GatePath(),
		"hardBlocks":   false,
	}
	changed := false
	for k, v := range want {
		// a decoded user file holds float64 and map[string]any; DeepEqual against our literals needs the same
		if !reflect.DeepEqual(cfg[k], v) {
			cfg[k], changed = v, true
		}
	}
	if !changed {
		return nil
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return fsx.Write(p, append(out, '\n'))
}
