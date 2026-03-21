package pluginhost

import (
	"testing"

	papi "github.com/xiaocaoooo/nyanyabot/internal/plugin"
)

type fakeProcess struct {
	killed bool
}

func (f *fakeProcess) Kill() {
	f.killed = true
}

func TestResolveDependencyOrderTopo(t *testing.T) {
	descs := map[string]papi.Descriptor{
		"external.a": {PluginID: "external.a", Dependencies: []string{"external.b"}},
		"external.b": {PluginID: "external.b", Dependencies: []string{}},
		"external.c": {PluginID: "external.c", Dependencies: []string{"external.b"}},
	}

	order, rejected := resolveDependencyOrder(descs, nil)
	if len(rejected) != 0 {
		t.Fatalf("unexpected rejected plugins: %#v", rejected)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 plugins in order, got %d (%v)", len(order), order)
	}

	pos := map[string]int{}
	for i, id := range order {
		pos[id] = i
	}
	if pos["external.b"] > pos["external.a"] {
		t.Fatalf("expected b before a, order=%v", order)
	}
	if pos["external.b"] > pos["external.c"] {
		t.Fatalf("expected b before c, order=%v", order)
	}
}

func TestResolveDependencyOrderMissingDependencySkipsOnlyAffected(t *testing.T) {
	descs := map[string]papi.Descriptor{
		"external.missing-dep": {PluginID: "external.missing-dep", Dependencies: []string{"external.not-exists"}},
		"external.ok":          {PluginID: "external.ok", Dependencies: []string{}},
	}

	order, rejected := resolveDependencyOrder(descs, nil)
	if _, ok := rejected["external.missing-dep"]; !ok {
		t.Fatalf("expected missing-dep plugin to be rejected, got %#v", rejected)
	}
	if _, ok := rejected["external.ok"]; ok {
		t.Fatalf("independent plugin should not be rejected, got %#v", rejected)
	}
	if len(order) != 1 || order[0] != "external.ok" {
		t.Fatalf("unexpected order: %v", order)
	}
}

func TestCycleRejectedPluginsAreClosed(t *testing.T) {
	descs := map[string]papi.Descriptor{
		"external.a": {PluginID: "external.a", Dependencies: []string{"external.b"}},
		"external.b": {PluginID: "external.b", Dependencies: []string{"external.a"}},
		"external.c": {PluginID: "external.c", Dependencies: []string{}},
	}

	order, rejected := resolveDependencyOrder(descs, nil)
	if len(order) != 1 || order[0] != "external.c" {
		t.Fatalf("expected only external.c to survive, got order=%v", order)
	}
	if _, ok := rejected["external.a"]; !ok {
		t.Fatalf("expected external.a rejected by cycle")
	}
	if _, ok := rejected["external.b"]; !ok {
		t.Fatalf("expected external.b rejected by cycle")
	}

	procA := &fakeProcess{}
	procB := &fakeProcess{}
	procC := &fakeProcess{}
	byID := map[string]*loadedCandidate{
		"external.a": {client: procA},
		"external.b": {client: procB},
		"external.c": {client: procC},
	}

	rejectCandidates(byID, rejected)

	if !procA.killed || !procB.killed {
		t.Fatalf("expected cycle plugins to be killed: a=%v b=%v", procA.killed, procB.killed)
	}
	if procC.killed {
		t.Fatalf("independent plugin should not be killed")
	}
	if _, ok := byID["external.a"]; ok {
		t.Fatalf("external.a should be removed from candidates")
	}
	if _, ok := byID["external.b"]; ok {
		t.Fatalf("external.b should be removed from candidates")
	}
	if _, ok := byID["external.c"]; !ok {
		t.Fatalf("external.c should remain in candidates")
	}
}
