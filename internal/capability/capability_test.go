package capability

import (
	"archive/tar"
	"bytes"
	"io"
	"slices"
	"strings"
	"testing"
)

func TestCatalogIsValid(t *testing.T) {
	if err := Check(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"expo", "android", "go", "bun", "rust", "python"} {
		if _, ok := Get(id); !ok {
			t.Errorf("the catalog lacks %s", id)
		}
	}
}

func TestUnionAndUnknown(t *testing.T) {
	if got := Union([]string{"go", "expo"}, nil, []string{"expo", " ", "rust"}); !slices.Equal(got, []string{"go", "expo", "rust"}) {
		t.Errorf("Union = %q", got)
	}
	if got := Unknown([]string{"expo", "cobol", "go"}); !slices.Equal(got, []string{"cobol"}) {
		t.Errorf("Unknown = %q", got)
	}
}

func TestResourcesOnlyRaiseLimits(t *testing.T) {
	if cpus, mem := Resources([]string{"go", "android"}, 1, 2048); cpus != 4 || mem != 8192 {
		t.Errorf("raised to %v CPUs, %d MB; want android's 4 and 8192", cpus, mem)
	}
	if cpus, mem := Resources([]string{"android"}, 0, 0); cpus != 0 || mem != 0 {
		t.Errorf("no limit became %v CPUs, %d MB; the provider's default should stay", cpus, mem)
	}
	if cpus, mem := Resources([]string{"expo"}, 8, 16384); cpus != 8 || mem != 16384 {
		t.Errorf("a higher limit was lowered to %v, %d", cpus, mem)
	}
}

func TestArchive(t *testing.T) {
	files := map[string]string{}
	r := tar.NewReader(bytes.NewReader(Archive()))
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(r)
		files[h.Name] = string(b)
	}
	for _, c := range Catalog {
		for _, f := range []string{"name", "hash", "install.sh", "detect.sh", "SKILL.md", "secrets", "env"} {
			if _, ok := files[c.ID+"/"+f]; !ok {
				t.Errorf("%s/%s is missing", c.ID, f)
			}
		}
		if !strings.HasPrefix(files[c.ID+"/SKILL.md"], "---\nname: "+c.ID+"\ndescription: ") {
			t.Errorf("%s's skill has no frontmatter: %q", c.ID, files[c.ID+"/SKILL.md"][:40])
		}
	}
	if files["expo/secrets"] != "EXPO_TOKEN optional an Expo access token, for EAS builds, updates and submissions\n" {
		t.Errorf("expo/secrets = %q", files["expo/secrets"])
	}
	if files["android/env"] != "ANDROID_HOME=$HOME/android-sdk\nJAVA_HOME=$HOME/.local/jdk-17\n" {
		t.Errorf("android/env = %q", files["android/env"])
	}
	if a, b := Catalog[0].Hash(), Catalog[0].Hash(); a != b || len(a) != 12 {
		t.Errorf("hash %q, %q", a, b)
	}
}
