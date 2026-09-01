package binding

import (
	"fmt"
	"reflect"
	"sort"

	"github.com/mikefsq/goalpaca/server"
)

// CheckTotal reports where tbl and iface disagree: every type-specific method
// needs exactly one entry, and every entry a method.
func CheckTotal(iface reflect.Type, tbl Table) []string {
	var problems []string

	common := map[string]bool{}
	dev := reflect.TypeOf((*server.Device)(nil)).Elem()
	for i := 0; i < dev.NumMethod(); i++ {
		common[dev.Method(i).Name] = true
	}

	want := map[string]bool{}
	for i := 0; i < iface.NumMethod(); i++ {
		name := iface.Method(i).Name
		if common[name] {
			continue
		}
		key := name
		// A SetXxx taking one argument is Xxx's write half and shares its
		// entry; SetPark (no args) is its own member.
		if len(name) > 3 && name[:3] == "Set" && iface.Method(i).Type.NumIn() == 1 {
			if _, hasBase := methodSet(iface)[name[3:]]; hasBase {
				key = name[3:]
			}
		}
		want[key] = true
		if _, ok := tbl[key]; !ok {
			problems = append(problems, fmt.Sprintf("no table entry for member %q (method %s)", key, name))
		}
	}
	for key, e := range tbl {
		if !want[key] {
			problems = append(problems, fmt.Sprintf("table entry %q matches no interface method", key))
		}
		if e.Kind == Absent && e.Why == "" {
			problems = append(problems, fmt.Sprintf("absent entry %q has no recorded reason", key))
		}
		if e.Kind == Mapped && (e.Prop == "" || e.Elem == "") {
			problems = append(problems, fmt.Sprintf("mapped entry %q lacks Prop/Elem", key))
		}
		if e.Kind == Func && e.Fn == "" {
			problems = append(problems, fmt.Sprintf("func entry %q names no function", key))
		}
	}
	sort.Strings(problems)
	return problems
}

func methodSet(iface reflect.Type) map[string]bool {
	m := map[string]bool{}
	for i := 0; i < iface.NumMethod(); i++ {
		m[iface.Method(i).Name] = true
	}
	return m
}
