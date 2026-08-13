package data

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestRegistryValidation(t *testing.T) {
	tests := []struct {
		name string
		test func(*Registry) error
		want error
	}{
		{
			name: "empty name",
			test: func(registry *Registry) error {
				return registry.Register("", newEmptySet)
			},
			want: ErrInvalidVersion,
		},
		{
			name: "nil factory",
			test: func(registry *Registry) error {
				return registry.Register("java/nil", nil)
			},
			want: ErrInvalidVersion,
		},
		{
			name: "duplicate registration",
			test: func(registry *Registry) error {
				if err := registry.Register("java/duplicate", newEmptySet); err != nil {
					return err
				}
				return registry.Register("java/duplicate", newEmptySet)
			},
			want: ErrDuplicateVersion,
		},
		{
			name: "unknown version",
			test: func(registry *Registry) error {
				_, err := registry.Load("java/missing")
				return err
			},
			want: ErrUnknownVersion,
		},
		{
			name: "nil set",
			test: func(registry *Registry) error {
				if err := registry.Register("java/nil-set", func() (*Set, error) { return nil, nil }); err != nil {
					return err
				}
				_, err := registry.Load("java/nil-set")
				return err
			},
			want: ErrNilSet,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.test(NewRegistry())
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.want)
			}
		})
	}
}

func TestRegistryFactoryErrorContext(t *testing.T) {
	want := errors.New("factory failed")
	registry := NewRegistry()
	if err := registry.Register("java/broken", func() (*Set, error) { return nil, want }); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	_, err := registry.Load("java/broken")
	if !errors.Is(err, want) {
		t.Fatalf("Load() error = %v, want wrapped factory error", err)
	}
	if !strings.Contains(err.Error(), "java/broken") {
		t.Fatalf("Load() error = %q, want version context", err)
	}
}

func TestRegistryVersionsSortedAndOwned(t *testing.T) {
	registry := NewRegistry()
	for _, name := range []string{"java/b", "java/a"} {
		if err := registry.Register(name, newEmptySet); err != nil {
			t.Fatalf("Register(%q) error = %v", name, err)
		}
	}

	want := []string{"java/a", "java/b"}
	versions := registry.Versions()
	if !reflect.DeepEqual(versions, want) {
		t.Fatalf("Versions() = %v, want %v", versions, want)
	}

	versions[0] = "changed"
	if got := registry.Versions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Versions() after caller mutation = %v, want %v", got, want)
	}
}

func TestRegistryFactoryIsolation(t *testing.T) {
	registry := NewRegistry()
	counter := 0
	if err := registry.Register("java/counting", func() (*Set, error) {
		counter++
		return NewSet(SetOptions{Version: Version{MinecraftVersion: strconv.Itoa(counter)}})
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	first, err := registry.Load("java/counting")
	if err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	second, err := registry.Load("java/counting")
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}

	if counter != 2 {
		t.Fatalf("factory call count = %d, want 2", counter)
	}
	if first == second {
		t.Fatal("Load() returned the same Set pointer twice")
	}
	if first.Version().MinecraftVersion == second.Version().MinecraftVersion {
		t.Fatalf("loaded versions are both %q, want distinct values", first.Version().MinecraftVersion)
	}
}

func TestRegistryZeroValue(t *testing.T) {
	var registry Registry
	if err := registry.Register("java/zero", newEmptySet); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := registry.Load("java/zero"); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestRegistryConcurrency(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("java/stable", newEmptySet); err != nil {
		t.Fatalf("Register(stable) error = %v", err)
	}

	const operations = 64
	errorsChannel := make(chan error, operations*3)
	var waitGroup sync.WaitGroup
	for index := range operations {
		waitGroup.Add(3)

		go func() {
			defer waitGroup.Done()
			name := fmt.Sprintf("java/concurrent-%03d", index)
			if err := registry.Register(name, newEmptySet); err != nil {
				errorsChannel <- fmt.Errorf("Register(%q): %w", name, err)
			}
		}()

		go func() {
			defer waitGroup.Done()
			versions := registry.Versions()
			if !slices.IsSorted(versions) {
				errorsChannel <- fmt.Errorf("Versions() returned unsorted values: %v", versions)
			}
		}()

		go func() {
			defer waitGroup.Done()
			if _, err := registry.Load("java/stable"); err != nil {
				errorsChannel <- fmt.Errorf("Load(stable): %w", err)
			}
		}()
	}
	waitGroup.Wait()
	close(errorsChannel)

	for err := range errorsChannel {
		t.Error(err)
	}
	if got, want := len(registry.Versions()), operations+1; got != want {
		t.Fatalf("len(Versions()) = %d, want %d", got, want)
	}
}

func TestRegistryPackageLevelWrappers(t *testing.T) {
	previousDefault := defaultRegistry
	defaultRegistry = NewRegistry()
	t.Cleanup(func() { defaultRegistry = previousDefault })

	const name = "test/task-3/package-level"
	if err := Register(name, newEmptySet); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if _, err := Load(name); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !slices.Contains(RegisteredVersions(), name) {
		t.Fatalf("RegisteredVersions() does not contain %q", name)
	}
}

func newEmptySet() (*Set, error) {
	return NewSet(SetOptions{})
}
