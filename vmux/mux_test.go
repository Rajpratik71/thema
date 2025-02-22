package vmux

import (
	gjson "encoding/json"
	"errors"
	"fmt"
	"testing"

	"cuelang.org/go/cue/cuecontext"
	"github.com/grafana/thema"
	"github.com/grafana/thema/exemplars"
	"github.com/grafana/thema/internal/envvars"
	"github.com/stretchr/testify/require"
)

func w[R any](r R, err error) func() (R, error) {
	return func() (R, error) {
		return r, err
	}
}

func errdie[R any](t *testing.T, f func() (R, error)) R {
	t.Helper()
	r, err := f()
	if err != nil {
		t.Fatal(err)
	}
	return r
}

type type00 struct {
	Before    string `json:"before"`
	Unchanged string `json:"unchanged"`
}
type type10 struct {
	After     string `json:"after"`
	Unchanged string `json:"unchanged"`
}

type renameLins[T0, T1 any] struct {
	first  thema.ConvergentLineage[T0]
	second thema.ConvergentLineage[T1]
}

func setupRenameLins(t *testing.T, rt *thema.Runtime) renameLins[*type00, *type10] {
	lin := errdie(t, w(exemplars.RenameLineage(rt)))
	sch1 := errdie(t, w(lin.Schema(thema.SV(0, 0))))
	sch2 := errdie(t, w(lin.Schema(thema.SV(1, 0))))

	var tf00 = type00{}
	var tf10 = type10{}
	return renameLins[*type00, *type10]{
		first:  errdie(t, w(thema.BindType[*type00](sch1, &tf00))).ConvergentLineage(),
		second: errdie(t, w(thema.BindType[*type10](sch2, &tf10))).ConvergentLineage(),
	}
}

// some raw data, valid at some particular schema version in a particular
// lineage, combined with the expected form that data will take after
// passing through translation to other schema versions in that lineage
type spectrum struct {
	// input to translation
	in arg
	// outputs of translation
	out codomain

	// Endec used to decode inputs and re-encode outputs
	endec Endec
}

// the input of a spectrum
type arg struct {
	v   thema.SyntacticVersion
	str string
}

// image, as in the basic maths notion of the output from a function
type image struct {
	str string
	lac thema.TranslationLacunas
	err error
}

// codomain represents the set of outputs from translation
type codomain map[thema.SyntacticVersion]image

func TestMuxers(t *testing.T) {
	ctx := cuecontext.New()
	rt := thema.NewRuntime(ctx)

	table := map[string]spectrum{
		"00good": {
			in: arg{
				str: `{
				"before": "renamedstr",
				"unchanged": "unchanged str val"
			}`,
			},
			out: codomain{
				thema.SV(1, 0): image{
					str: `{
	"after": "renamedstr",
	"unchanged": "unchanged str val"
}`,
				},
			},
		},
		"10good": {
			in: arg{
				v: thema.SV(1, 0),
				str: `{
				"after": "renamedstr",
				"unchanged": "unchanged str val"
			}`,
			},
			out: codomain{
				thema.SV(0, 0): image{
					str: `{
	"before": "renamedstr",
	"unchanged": "unchanged str val"
}`,
				},
			},
		},
		"00empty": {
			in: arg{
				str: `{
				"before": "",
				"unchanged": ""
			}`,
			},
			out: codomain{
				thema.SV(1, 0): image{
					str: `{
	"after": "",
	"unchanged": ""
}`,
				},
			},
		},
		"10empty": {
			in: arg{
				v: thema.SV(1, 0),
				str: `{
				"after": "",
				"unchanged": ""
			}`,
			},
			out: codomain{
				thema.SV(0, 0): image{
					str: `{
	"before": "",
	"unchanged": ""
}`,
				},
			},
		},
	}

	for n, spec := range table {
		spec.endec = NewJSONEndec("test")
		table[n] = spec
	}

	lins := setupRenameLins(t, rt)
	t.Run("firsttyped", func(t *testing.T) {
		for name, item := range table {
			spec := item
			t.Run(name, func(t *testing.T) {
				checkSpectrumAcrossMuxers(t, lins.first, spec)
			})
		}
	})
	t.Run("secondtyped", func(t *testing.T) {
		for name, item := range table {
			spec := item
			t.Run(name, func(t *testing.T) {
				checkSpectrumAcrossMuxers(t, lins.second, spec)
			})
		}
	})
}

func checkSpectrumAcrossMuxers[T thema.Assignee](t *testing.T, clin thema.ConvergentLineage[T], spec spectrum) {
	t.Parallel()
	sch := errdie(t, w(clin.Schema(thema.SV(0, 0))))
	vmap := make(map[thema.SyntacticVersion]bool)
	for ; sch != nil; sch = sch.Successor() {
		vmap[sch.Version()] = false
	}

	if _, has := vmap[spec.in.v]; !has {
		t.Fatalf("spectrum specifies input is for schema %v, but lineage contains no such schema", spec.in.v)
	}
	vmap[spec.in.v] = true
	for v, _ := range spec.out {
		if _, has := vmap[v]; !has {
			t.Fatalf("spectrum specifies output for schema %v, but lineage contains no such schema", v)
		}
		vmap[v] = true
	}

	// All versions in the lineage should now be accounted for as either inputs or outputs
	for v, matched := range vmap {
		if !matched {
			t.Fatalf("no input or output in spectrum for lineage schema version %v", v)
		}
	}

	concctx := cuecontext.New()
	tsch := clin.TypedSchema()
	for v, img := range spec.out {
		if v == spec.in.v {
			continue
		}
		// Normalize string form of output to avoid spurious errors
		img.str = string(errdie(t, w(spec.endec.Encode(
			errdie(t, w(spec.endec.Decode(concctx, []byte(img.str))))))))

		t.Run(fmt.Sprintf("%v->%v", spec.in.v, v), func(t *testing.T) {
			t.Parallel()
			if !envvars.ReverseTranslate && v.Less(spec.in.v) {
				t.Skip("thema does not yet support reverse translation")
			}

			// Always do the untyped muxers
			t.Run("UntypedMux", func(T *testing.T) {
				um := NewUntypedMux(thema.SchemaP(clin, v), spec.endec)
				inst, lac, err := um([]byte(spec.in.str))
				handleLE(t, img, lac, err)

				final := errdie(t, w(spec.endec.Encode(inst.UnwrapCUE())))
				require.Equal(t, img.str, string(final))
			})
			t.Run("ByteMux", func(T *testing.T) {
				um := NewByteMux(thema.SchemaP(clin, v), spec.endec)
				final, lac, err := um([]byte(spec.in.str))
				handleLE(t, img, lac, err)

				require.Equal(t, img.str, string(final))
			})

			// Do the typed muxers only if this is the convergent schema for the lineage
			if v == tsch.Version() {
				t.Run("TypedMux", func(t *testing.T) {
					um := NewTypedMux(tsch, spec.endec)
					inst, lac, err := um([]byte(spec.in.str))
					handleLE(t, img, lac, err)

					final := errdie(t, w(spec.endec.Encode(inst.UnwrapCUE())))
					require.Equal(t, img.str, string(final))
				})
				t.Run("TypedMux", func(t *testing.T) {
					// No easy way to go from a pure Go type back to CUE, so
					// just hardcode to the builtin JSON endec and skip otherwise
					if _, is := spec.endec.(jsonEndec); !is {
						t.Skipf("generic testing of TypedMux only works with the jsonEndec, got %T", spec.endec)
					}

					um := NewValueMux(tsch, spec.endec)
					inst, lac, err := um([]byte(spec.in.str))
					handleLE(t, img, lac, err)

					final := errdie(t, w(gjson.Marshal(inst)))
					require.Equal(t, img.str, string(final))
				})
			}
		})
	}
}

// handler for lacunas and errors
func handleLE(t *testing.T, im image, lac thema.TranslationLacunas, err error) {
	t.Helper()
	if err != nil && im.err == nil {
		t.Fatalf("unexpected error while muxing: %s", err)
	} else if im.err != nil && err == nil {
		t.Fatalf("muxing raised no error, but expected %s", im.err)
	} else if err != nil && !errors.Is(err, im.err) { // TODO probably need more smarts than errors.Is
		t.Fatalf("received and expected errors differ:\n\tGOT: %s\n\tWANT: %s", err, im.err)
	}

	// TODO For now, pass this off to require. Totally needs special handling, though
	// require.EqualValues(t, im.lac, lac)
}
