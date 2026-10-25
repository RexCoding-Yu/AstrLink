package plugininstall

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// The icons of task plugins that name a LobeHub icon come from LobeHub's
// static SVG package, a pinned version checked against its hash, which the
// installer keeps once fetched.
const (
	lobeIconsPackage   = "/@lobehub/icons-static-svg/-/icons-static-svg-1.98.0.tgz"
	lobeIconsIntegrity = "sha512-soyLV2g74ujV+9BBymPwXrorJ+kQsEz7WvVd4uzhN2wZnd4Jbyc2FsmyjZBosGaufOirCs1RGJQlLe6l9bjwLg=="
	lobeIconsCache     = "lobehub-icons-static-svg-1.98.0.tgz"
	cacheDir           = ".cache"
)

// lobeIconPattern is a LobeHub icon name, such as Sora.Color or XAI.
var lobeIconPattern = regexp.MustCompile(`^[A-Za-z0-9]+(?:\.[A-Za-z0-9]+)?$`)

// lobeIconFile is the file of a LobeHub icon in the package: its name in
// lower case, a variant after a hyphen.
func lobeIconFile(name string) (string, bool) {
	if !lobeIconPattern.MatchString(name) {
		return "", false
	}
	return "package/icons/" + strings.ToLower(strings.ReplaceAll(name, ".", "-")) + ".svg", true
}

// lobeIcons fetches the icon package once per installer.
type lobeIcons struct {
	mutex   sync.Mutex
	archive []byte
}

// lobeIcon returns the SVG of a LobeHub icon name.
func (installer *Installer) lobeIcon(ctx context.Context, name string) ([]byte, error) {
	file, ok := lobeIconFile(name)
	if !ok {
		return nil, errors.New("not a LobeHub icon name")
	}
	archive, err := installer.lobeArchive(ctx)
	if err != nil {
		return nil, err
	}
	unzipped, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	defer unzipped.Close()
	reader := tar.NewReader(unzipped)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("LobeHub has no icon " + name)
		}
		if err != nil {
			return nil, err
		}
		if header.Name == file && header.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(reader, maxIconBytes+1))
		}
	}
}

// maxIconBytes bounds an icon the installer keeps.
const maxIconBytes = 64 << 10

// lobeArchive is the icon package: from memory, from the installer's
// cache, or fetched from the npm registries.
func (installer *Installer) lobeArchive(ctx context.Context) ([]byte, error) {
	installer.icons.mutex.Lock()
	defer installer.icons.mutex.Unlock()
	if installer.icons.archive != nil {
		return installer.icons.archive, nil
	}
	check, err := parseIntegrity(lobeIconsIntegrity)
	if err != nil {
		return nil, err
	}
	cache := ""
	if installer.options.Dir != "" {
		cache = filepath.Join(installer.options.Dir, cacheDir, lobeIconsCache)
		if data, err := os.ReadFile(cache); err == nil && check(data) {
			installer.icons.archive = data
			return data, nil
		}
	}
	fetcher := &registryFetcher{client: installer.options.Client, registries: installer.registries()}
	data, err := fetcher.fetch(ctx, lobeIconsPackage, lobeIconsIntegrity, "@lobehub/icons-static-svg")
	if err != nil {
		return nil, err
	}
	if cache != "" {
		if err := os.MkdirAll(filepath.Dir(cache), 0o700); err == nil {
			_ = os.WriteFile(cache, data, 0o600)
		}
	}
	installer.icons.archive = data
	return data, nil
}
