package starcfg

import (
	"fmt"

	"go.starlark.net/starlark"

	"github.com/stevenzg/tarjan/internal/config"
)

// isNone reports whether v is unset (nil or Starlark None).
func isNone(v starlark.Value) bool {
	if v == nil {
		return true
	}
	_, ok := v.(starlark.NoneType)
	return ok
}

// toStrings converts a Starlark iterable of strings to a Go slice.
func toStrings(name string, v starlark.Value) ([]string, error) {
	if isNone(v) {
		return nil, nil
	}
	iter, ok := v.(starlark.Iterable)
	if !ok {
		return nil, fmt.Errorf("%s: expected a list of strings, got %s", name, v.Type())
	}
	it := iter.Iterate()
	defer it.Done()
	var out []string
	var x starlark.Value
	for it.Next(&x) {
		s, ok := starlark.AsString(x)
		if !ok {
			return nil, fmt.Errorf("%s: expected strings, got %s", name, x.Type())
		}
		out = append(out, s)
	}
	return out, nil
}

// toStrMap converts a Starlark dict of string->string to a Go map.
func toStrMap(name string, v starlark.Value) (map[string]string, error) {
	if isNone(v) {
		return nil, nil
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("%s: expected a dict, got %s", name, v.Type())
	}
	out := make(map[string]string, d.Len())
	for _, item := range d.Items() {
		k, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("%s: keys must be strings", name)
		}
		val, ok := starlark.AsString(item[1])
		if !ok {
			return nil, fmt.Errorf("%s[%q]: value must be a string", name, k)
		}
		out[k] = val
	}
	return out, nil
}

// toRemotes converts a Starlark dict of name -> remote(...) into the config map.
func toRemotes(name string, v starlark.Value) (map[string]config.Remote, error) {
	if isNone(v) {
		return nil, nil
	}
	d, ok := v.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("%s: expected a dict of name -> remote(...), got %s", name, v.Type())
	}
	out := make(map[string]config.Remote, d.Len())
	for _, item := range d.Items() {
		k, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("%s: keys must be strings", name)
		}
		w, ok := item[1].(wrapped)
		if !ok || w.typeName != "remote" {
			return nil, fmt.Errorf("%s[%q]: expected remote(...), got %s", name, k, item[1].Type())
		}
		out[k] = w.v.(config.Remote)
	}
	return out, nil
}

// toInstall accepts either a string (one command) or a dict (per-OS) and builds
// an InstallSpec.
func toInstall(name string, v starlark.Value) (config.InstallSpec, error) {
	if isNone(v) {
		return config.InstallSpec{}, nil
	}
	if s, ok := starlark.AsString(v); ok {
		return config.NewInstall(s, nil), nil
	}
	if _, ok := v.(*starlark.Dict); ok {
		perOS, err := toStrMap(name, v)
		if err != nil {
			return config.InstallSpec{}, err
		}
		return config.NewInstall("", perOS), nil
	}
	return config.InstallSpec{}, fmt.Errorf("%s: expected a command string or a per-OS dict", name)
}

// toPackage accepts either a string (one package name) or a dict (per-manager)
// and builds a PackageSpec.
func toPackage(name string, v starlark.Value) (config.PackageSpec, error) {
	if isNone(v) {
		return config.PackageSpec{}, nil
	}
	if s, ok := starlark.AsString(v); ok {
		return config.NewPackage(s, nil), nil
	}
	if _, ok := v.(*starlark.Dict); ok {
		perMgr, err := toStrMap(name, v)
		if err != nil {
			return config.PackageSpec{}, err
		}
		return config.NewPackage("", perMgr), nil
	}
	return config.PackageSpec{}, fmt.Errorf("%s: expected a package name or a per-manager dict", name)
}

// optWrapped extracts the Go value from an optional wrapped argument, checking
// its kind.
func optWrapped(name, want string, v starlark.Value) (any, error) {
	if isNone(v) {
		return nil, nil
	}
	w, ok := v.(wrapped)
	if !ok || w.typeName != want {
		return nil, fmt.Errorf("%s: expected %s(...), got %s", name, want, v.Type())
	}
	return w.v, nil
}

// optInt extracts an optional int argument.
func optInt(name string, v starlark.Value) (int, bool, error) {
	if isNone(v) {
		return 0, false, nil
	}
	i, ok := v.(starlark.Int)
	if !ok {
		return 0, false, fmt.Errorf("%s: expected an int, got %s", name, v.Type())
	}
	n, ok := i.Int64()
	if !ok {
		return 0, false, fmt.Errorf("%s: int out of range", name)
	}
	return int(n), true, nil
}

// unwrapInto iterates a Starlark list of wrapped values of the given kind,
// passing each underlying Go value to add.
func unwrapInto(name, want string, v starlark.Value, add func(any)) error {
	if isNone(v) {
		return nil
	}
	iter, ok := v.(starlark.Iterable)
	if !ok {
		return fmt.Errorf("%s: expected a list of %s(...), got %s", name, want, v.Type())
	}
	it := iter.Iterate()
	defer it.Done()
	var x starlark.Value
	for it.Next(&x) {
		w, ok := x.(wrapped)
		if !ok || w.typeName != want {
			return fmt.Errorf("%s: expected %s(...) values, got %s", name, want, x.Type())
		}
		add(w.v)
	}
	return nil
}
