package exemplars

import (
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"testing/fstest"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/load"
	"github.com/grafana/thema"
	"github.com/grafana/thema/internal/util"
	tload "github.com/grafana/thema/load"
)

// All returns all of the exemplar lineages in a map keyed by lineage name.
func All(rt *thema.Runtime) map[string]thema.Lineage {
	all := make(map[string]thema.Lineage)
	iter, err := buildAll(rt.Context()).Fields(cue.Definitions(false))
	if err != nil {
		panic(err)
	}

	for iter.Next() {
		v := iter.Value().LookupPath(cue.ParsePath("l"))
		name, _ := v.LookupPath(cue.ParsePath("name")).String()

		lin, err := thema.BindLineage(v, rt, nameOpts[name]...)
		if err != nil {
			panic(err)
		}
		all[name] = lin
	}

	return all
}

var nameOpts = map[string][]thema.BindOption{
	"defaultchange": {thema.SkipBuggyChecks()},
	"narrowing":     {},
	"rename":        {},
	"expand":        {},
	"single":        {},
}

func buildAll(ctx *cue.Context) cue.Value {
	all, err := tload.InstancesWithThema(CueFS(), ".")
	if err != nil {
		panic(err)
	}
	return ctx.BuildInstance(all)
}

// CueFS returns an fs.FS containing the .cue files, along with a simulated
// cue.mod directory, making it suitable for use with load.InstancesWithThema().
func CueFS() fs.FS {
	m, err := populateMapFSFromRoot(cueFS, "", "")
	if err != nil {
		panic(fmt.Sprintf("broken mapfs: %s", err))
	}

	m["cue.mod/module.cue"] = &fstest.MapFile{
		Data: []byte("module: \"github.com/grafana/thema/exemplars\""),
	}
	return m
}

func populateMapFSFromRoot(in fs.FS, root, join string) (fstest.MapFS, error) {
	out := make(fstest.MapFS)
	err := fs.WalkDir(in, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		// Ignore gosec warning G304. The input set here is necessarily
		// constrained to files specified in embed.go
		// nolint:gosec
		b, err := in.Open(filepath.Join(root, join, path))
		if err != nil {
			return err
		}
		defer b.Close() // nolint: errcheck

		byt, err := io.ReadAll(b)
		if err != nil {
			return err
		}

		out[path] = &fstest.MapFile{Data: byt}
		return nil
	})
	return out, err
}

// NarrowingLineage returns a handle for using the "narrowing" exemplar lineage.
func NarrowingLineage(rt *thema.Runtime, o ...thema.BindOption) (thema.Lineage, error) {
	return lineageForExemplar("narrowing", rt, o...)
}

// RenameLineage returns a handle for using the "Rename" exemplar lineage.
func RenameLineage(rt *thema.Runtime, o ...thema.BindOption) (thema.Lineage, error) {
	return lineageForExemplar("rename", rt, o...)
}

// DefaultChangeLineage returns a handle for using the "defaultchange" exemplar lineage.
func DefaultChangeLineage(rt *thema.Runtime, o ...thema.BindOption) (thema.Lineage, error) {
	return lineageForExemplar("defaultchange", rt, o...)
}

// ExpandLineage returns a handle for using the "expand" exemplar lineage.
func ExpandLineage(rt *thema.Runtime, o ...thema.BindOption) (thema.Lineage, error) {
	return lineageForExemplar("expand", rt, o...)
}

// SingleLineage returns a handle for using the "single" exemplar lineage.
func SingleLineage(rt *thema.Runtime, o ...thema.BindOption) (thema.Lineage, error) {
	return lineageForExemplar("single", rt, o...)
}

var _ thema.LineageFactory = NarrowingLineage
var _ thema.LineageFactory = RenameLineage
var _ thema.LineageFactory = DefaultChangeLineage
var _ thema.LineageFactory = ExpandLineage
var _ thema.LineageFactory = SingleLineage

// Build the harness containing a single exemplar lineage.
func harnessForExemplar(name string, rt *thema.Runtime) cue.Value {
	all := buildExemplarsPackage(rt)

	lval := all.LookupPath(cue.MakePath(cue.Str(name)))
	if !lval.Exists() {
		panic(fmt.Sprintf("no exemplar exists with name %q", name))
	}

	return lval.LookupPath(cue.MakePath(cue.Str("l")))
}

// Build a Lineage representing a single exemplar.
func lineageForExemplar(name string, rt *thema.Runtime, o ...thema.BindOption) (thema.Lineage, error) {
	switch name {
	case "defaultchange", "narrowing", "rename":
		o = append(o, thema.SkipBuggyChecks())
	}
	return thema.BindLineage(harnessForExemplar(name, rt), rt, o...)
}

func buildExemplarsPackage(rt *thema.Runtime) cue.Value {
	ctx := rt.UnwrapCUE().Context()

	overlay, err := exemplarOverlay()
	if err != nil {
		panic(err)
	}

	cfg := &load.Config{
		Overlay: overlay,
		Module:  "github.com/grafana/thema",
		Dir:     filepath.Join(util.Prefix, "exemplars"),
	}

	return ctx.BuildInstance(load.Instances(nil, cfg)[0])
}

func exemplarOverlay() (map[string]load.Source, error) {
	overlay := make(map[string]load.Source)

	if err := util.ToOverlay(util.Prefix, thema.CueJointFS, overlay); err != nil {
		return nil, err
	}

	if err := util.ToOverlay(filepath.Join(util.Prefix, "exemplars"), cueFS, overlay); err != nil {
		return nil, err
	}

	return overlay, nil
}
