package plugininstall

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/plugin"
	"github.com/QuantumNous/astrlink/core/internal/pluginbuild"
)

// A plugin marketplace is new-api's index format: an index.json that lists
// each plugin's versions, the file of each relative to the index, and its
// sha256. A marketplace link names a plugin of an index, and a version:
// https://…/index.json#key@version, the newest without one.

// Bounds of what a marketplace install reads.
const (
	maxIndexBytes = 8 << 20
	// marketCacheFor is how long a marketplace's listing is reused.
	marketCacheFor = 5 * time.Minute
)

type marketIndex struct {
	IndexVersion int            `json:"indexVersion"`
	Name         string         `json:"name"`
	Plugins      []marketPlugin `json:"plugins"`
}

type marketPlugin struct {
	Key          string                     `json:"key"`
	Name         string                     `json:"name"`
	Icon         string                     `json:"icon"`
	IconFile     *marketFile                `json:"iconFile"`
	Description  plugin.LocalizedText       `json:"description"`
	Website      string                     `json:"website"`
	SortPriority int32                      `json:"sortPriority"`
	Models       []string                   `json:"models"`
	Latest       string                     `json:"latest"`
	Versions     []marketVersion            `json:"versions"`
	Routes       []plugin.TaskRoute         `json:"routes"`
	Protocols    []plugin.TaskProtocolClaim `json:"protocols"`
}

type marketFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type marketVersion struct {
	Version       string          `json:"version"`
	Path          string          `json:"path"`
	SHA256        string          `json:"sha256"`
	MinAPIVersion int             `json:"minApiVersion"`
	Kind          string          `json:"kind"`
	AllowedHosts  []string        `json:"allowedHosts"`
	BaseURL       string          `json:"baseUrl"`
	Auth          plugin.TaskAuth `json:"auth"`
}

func parseIndex(data []byte) (*marketIndex, error) {
	var index marketIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, errors.New("the marketplace's index.json is not an index")
	}
	if index.IndexVersion != 1 {
		return nil, fmt.Errorf("the marketplace's index is version %d, not 1", index.IndexVersion)
	}
	return &index, nil
}

// find returns a plugin of the index and one of its versions: version, or
// the newest for "".
func (index *marketIndex) find(key, version string) (*marketPlugin, *marketVersion) {
	for at := range index.Plugins {
		entry := &index.Plugins[at]
		if entry.Key != key {
			continue
		}
		if version == "" {
			version = entry.Latest
		}
		for v := range entry.Versions {
			if entry.Versions[v].Version == version {
				return entry, &entry.Versions[v]
			}
		}
		return entry, nil
	}
	return nil, nil
}

// MarketLink is the link that installs version of key from index.
func MarketLink(index, key, version string) string {
	link := strings.SplitN(index, "#", 2)[0] + "#" + key
	if version != "" {
		link += "@" + version
	}
	return link
}

// marketRef is what a marketplace link names.
type marketRef struct {
	index   string
	key     string
	version string
}

// parseMarketLink reads a marketplace link: an http or https address of
// an index.json, and the plugin's key and version after #.
func parseMarketLink(text string) (marketRef, bool) {
	parsed, err := url.Parse(strings.TrimSpace(text))
	if err != nil || parsed.Host == "" || parsed.User != nil || !strings.HasSuffix(parsed.Path, ".json") {
		return marketRef{}, false
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && loopbackHost(parsed.Hostname())) {
		return marketRef{}, false
	}
	key, version, _ := strings.Cut(parsed.Fragment, "@")
	if !plugin.ValidPluginKey(key) {
		return marketRef{}, false
	}
	parsed.Fragment, parsed.RawFragment = "", ""
	return marketRef{index: parsed.String(), key: key, version: version}, true
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// marketGet reads one file of a marketplace, up to limit.
func (installer *Installer) marketGet(ctx context.Context, address string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := installer.options.Client.Do(request)
	if err != nil {
		return nil, fail(contract.PluginInstallUnavailable, "marketplace: "+networkError(err))
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == http.StatusNotFound:
		return nil, fail(contract.PluginInstallNoPlugin, address)
	case response.StatusCode != http.StatusOK:
		return nil, fail(contract.PluginInstallUnavailable, fmt.Sprintf("marketplace: %s answered %d", request.URL.Host, response.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fail(contract.PluginInstallUnavailable, "marketplace: "+networkError(err))
	}
	if int64(len(data)) > limit {
		return nil, fail(contract.PluginInstallTooLarge, path.Base(request.URL.Path))
	}
	return data, nil
}

// fetchIndex reads a marketplace's index, the official one from jsDelivr
// when GitHub cannot be reached, and returns the address it came from,
// which the files it lists are relative to.
func (installer *Installer) fetchIndex(ctx context.Context, address string) (*marketIndex, string, error) {
	addresses := []string{address}
	if address == contract.OfficialPluginMarket {
		addresses = append(addresses, contract.OfficialPluginMarketFallback)
	}
	var last error
	for _, candidate := range addresses {
		data, err := installer.marketGet(ctx, candidate, maxIndexBytes)
		if err != nil {
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			last = err
			continue
		}
		index, err := parseIndex(data)
		if err != nil {
			return nil, "", &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{err.Error()}}}
		}
		return index, candidate, nil
	}
	return nil, "", last
}

// relative resolves a path of an index against the index's address,
// refusing one that leaves the marketplace's host.
func relative(base, name string) (string, error) {
	root, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	target, err := root.Parse(name)
	if err != nil || target.Host != root.Host || target.Scheme != root.Scheme {
		return "", errors.New("the index names a file outside the marketplace")
	}
	return target.String(), nil
}

// verified fetches a file of a marketplace and checks it against the
// sha256 the index lists.
func (installer *Installer) verified(ctx context.Context, base, name, sum string, limit int64) ([]byte, error) {
	address, err := relative(base, name)
	if err != nil {
		return nil, fail(contract.PluginInstallInvalidLink, name)
	}
	data, err := installer.marketGet(ctx, address, limit)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), sum) {
		return nil, fail(contract.PluginInstallIntegrity, name)
	}
	return data, nil
}

// previewMarket stages a plugin a marketplace link names.
func (installer *Installer) previewMarket(ctx context.Context, ref marketRef, link, work string, existing func(string) *Existing) (contract.PluginInstallReview, contract.PluginInstallRecord, error) {
	index, base, err := installer.fetchIndex(ctx, ref.index)
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	entry, version := index.find(ref.key, ref.version)
	switch {
	case entry == nil:
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallNoPlugin, ref.key)
	case version == nil:
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallRefNotFound, ref.key+"@"+ref.version)
	case version.Kind != "" && version.Kind != "task":
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, fail(contract.PluginInstallIncompatible, version.Kind)
	}
	text, err := installer.verified(ctx, base, version.Path, version.SHA256, plugin.MaxSourceBytes)
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	files := taskFiles{changelogs: map[string][]byte{}}
	if entry.IconFile != nil {
		if icon, err := installer.verified(ctx, base, entry.IconFile.Path, entry.IconFile.SHA256, plugin.MaxIconBytes); err == nil {
			files.icon, files.iconName = icon, "icon.svg"
			if strings.HasSuffix(strings.ToLower(entry.IconFile.Path), ".png") {
				files.iconName = "icon.png"
			}
		}
	}
	installer.marketChangelogs(ctx, base, version.Path, files.changelogs)
	staged, err := installer.stageTask(ctx, string(text), files, filepath.Join(work, "out"))
	if err != nil {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, err
	}
	if staged.checked.Task.Key != entry.Key {
		return contract.PluginInstallReview{}, contract.PluginInstallRecord{}, &Failure{contract.PluginInstallFailure{Reason: contract.PluginInstallInvalidPlugin, Messages: []string{"the plugin's key is not the one the index lists"}}}
	}
	name := index.Name
	if name == "" {
		name = ref.index
	}
	record := contract.PluginInstallRecord{
		Source: contract.PluginInstallSource{
			Kind: contract.PluginSourceMarket, Repository: name, URL: link, Index: ref.index, Key: entry.Key,
			Ref: version.Version, RefKind: contract.PluginRefVersion, Commit: strings.ToLower(version.SHA256), Subdir: version.Path,
		},
		Version: staged.checked.Task.Version, Outputs: staged.outputs,
	}
	review := installer.taskReview(staged, len(text), record, files)
	if existing != nil {
		compare(&review, record, existing(entry.Key))
	}
	return review, record, nil
}

// marketChangelogs fetches the changelog beside a version's file, and the
// translations its front matter names.
func (installer *Installer) marketChangelogs(ctx context.Context, base, file string, into map[string][]byte) {
	dir := path.Dir(file)
	canonical, err := installer.relativeGet(ctx, base, path.Join(dir, "CHANGELOG.md"))
	if err != nil {
		return
	}
	into["CHANGELOG.md"] = canonical
	for _, translation := range changelogTranslations(string(canonical)) {
		if data, err := installer.relativeGet(ctx, base, path.Join(dir, translation)); err == nil {
			into[translation] = data
		}
	}
}

func (installer *Installer) relativeGet(ctx context.Context, base, name string) ([]byte, error) {
	address, err := relative(base, name)
	if err != nil {
		return nil, err
	}
	return installer.marketGet(ctx, address, maxChangelogBytes)
}

// changelogTranslations reads the translations map of a changelog's front
// matter: the sibling files of other languages.
func changelogTranslations(text string) []string {
	if !strings.HasPrefix(text, "---") {
		return nil
	}
	var names []string
	inTranslations := false
	for _, line := range strings.Split(text, "\n")[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" {
			break
		}
		switch {
		case strings.HasPrefix(line, "translations:"):
			inTranslations = true
		case inTranslations && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")):
			_, value, found := strings.Cut(trimmed, ":")
			value = strings.Trim(strings.TrimSpace(value), `"'`)
			if found && changelogPattern.MatchString(value) && value != "CHANGELOG.md" {
				names = append(names, value)
			}
		default:
			inTranslations = false
		}
	}
	return names
}

// checkMarketUpdate looks for a newer version of a plugin installed from a
// marketplace: the newest its index lists.
func (installer *Installer) checkMarketUpdate(ctx context.Context, record contract.PluginInstallRecord) (contract.PluginUpdateCheck, error) {
	source := record.Source
	index, base, err := installer.fetchIndex(ctx, source.Index)
	if err != nil {
		return contract.PluginUpdateCheck{}, installer.failure(ctx, err)
	}
	entry, latest := index.find(source.Key, "")
	check := contract.PluginUpdateCheck{Ref: record.Version, RefKind: contract.PluginRefVersion}
	if entry == nil || latest == nil {
		return check, nil
	}
	current, okCurrent := pluginbuild.ParseVersion(record.Version)
	newest, okNewest := pluginbuild.ParseVersion(latest.Version)
	if !okCurrent || !okNewest || current.Compare(newest) >= 0 {
		return check, nil
	}
	check.Available, check.Version, check.Ref = true, latest.Version, latest.Version
	check.Commit = strings.ToLower(latest.SHA256)
	check.URL = MarketLink(source.Index, source.Key, latest.Version)
	changelogs := map[string][]byte{}
	installer.marketChangelogs(ctx, base, latest.Path, changelogs)
	check.Notes = changelogIn(changelogs, installer.language())
	return check, nil
}

// marketListing is a marketplace's listing as the installer last read it.
type marketListing struct {
	at      time.Time
	market  contract.PluginMarket
	entries []marketPlugin
}

type marketCache struct {
	mutex    sync.Mutex
	listings map[string]marketListing
	icons    map[string]string
}

// Market lists what a marketplace offers, the official one for "":
// newest version, models, protocols, and an icon to show. installed tells
// the version of a key installed now.
func (installer *Installer) Market(ctx context.Context, index string, installed func(key string) string) (contract.PluginMarket, error) {
	if index == "" {
		index = contract.OfficialPluginMarket
	}
	if _, ok := parseMarketLink(MarketLink(index, "probe", "")); !ok {
		return contract.PluginMarket{}, fail(contract.PluginInstallInvalidLink, index)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	installer.market.mutex.Lock()
	cached, ok := installer.market.listings[index]
	installer.market.mutex.Unlock()
	if !ok || installer.options.Now().Sub(cached.at) > marketCacheFor {
		listing, err := installer.readMarket(ctx, index)
		if err != nil {
			return contract.PluginMarket{}, installer.failure(ctx, err)
		}
		cached = listing
		installer.market.mutex.Lock()
		if installer.market.listings == nil {
			installer.market.listings = map[string]marketListing{}
		}
		installer.market.listings[index] = cached
		installer.market.mutex.Unlock()
	}
	market := cached.market
	market.Plugins = slices.Clone(market.Plugins)
	for at := range market.Plugins {
		if installed != nil {
			market.Plugins[at].Installed = installed(market.Plugins[at].Key)
		}
	}
	return market, nil
}

// readMarket reads a marketplace's index and the icons of its plugins.
func (installer *Installer) readMarket(ctx context.Context, address string) (marketListing, error) {
	index, base, err := installer.fetchIndex(ctx, address)
	if err != nil {
		return marketListing{}, err
	}
	language := installer.language()
	market := contract.PluginMarket{Index: address, Name: index.Name, Plugins: []contract.PluginMarketEntry{}}
	for _, entry := range index.Plugins {
		if !plugin.ValidPluginKey(entry.Key) || len(entry.Versions) == 0 {
			continue
		}
		listed := contract.PluginMarketEntry{
			Key: entry.Key, Name: entry.Name, Description: entry.Description.In(language), Icon: entry.Icon, Website: entry.Website,
			SortPriority: entry.SortPriority, Latest: entry.Latest, Models: slices.Clone(entry.Models),
			Protocols: []contract.PluginTaskProtocol{}, Routes: []contract.PluginTaskRoute{}, Versions: []contract.PluginMarketVersion{},
			Link: MarketLink(address, entry.Key, entry.Latest),
		}
		if listed.Models == nil {
			listed.Models = []string{}
		}
		for _, claim := range entry.Protocols {
			listed.Protocols = append(listed.Protocols, contract.PluginTaskProtocol{Name: claim.Name, Supports: claim.Supports, Models: claim.Models})
		}
		for _, route := range entry.Routes {
			listed.Routes = append(listed.Routes, contract.PluginTaskRoute{Method: route.Method, Path: route.Path, Type: route.Type, Models: route.Models})
		}
		for _, version := range entry.Versions {
			if version.Kind != "" && version.Kind != "task" {
				continue
			}
			listed.Versions = append(listed.Versions, contract.PluginMarketVersion{
				Version: version.Version, MinAPIVersion: version.MinAPIVersion, BaseURL: version.BaseURL,
				Auth: string(version.Auth.Kind()), AllowedHosts: version.AllowedHosts, Link: MarketLink(address, entry.Key, version.Version),
			})
		}
		if len(listed.Versions) == 0 {
			continue
		}
		listed.IconURL = installer.marketIcon(ctx, base, entry)
		market.Plugins = append(market.Plugins, listed)
	}
	// Each source sorts by descending priority, then key.
	sort.SliceStable(market.Plugins, func(i, j int) bool {
		if market.Plugins[i].SortPriority != market.Plugins[j].SortPriority {
			return market.Plugins[i].SortPriority > market.Plugins[j].SortPriority
		}
		return market.Plugins[i].Key < market.Plugins[j].Key
	})
	return marketListing{at: installer.options.Now(), market: market, entries: index.Plugins}, nil
}

// marketIcon is a marketplace plugin's icon as a data URL: the file the
// index lists, else its LobeHub icon; "" for a text avatar or an icon that
// could not be fetched.
func (installer *Installer) marketIcon(ctx context.Context, base string, entry marketPlugin) string {
	cacheKey := entry.Icon
	if entry.IconFile != nil {
		cacheKey = entry.IconFile.SHA256
	}
	installer.market.mutex.Lock()
	if icon, ok := installer.market.icons[cacheKey]; ok {
		installer.market.mutex.Unlock()
		return icon
	}
	installer.market.mutex.Unlock()
	var data []byte
	mime := "image/svg+xml"
	switch {
	case entry.IconFile != nil:
		fetched, err := installer.verified(ctx, base, entry.IconFile.Path, entry.IconFile.SHA256, plugin.MaxIconBytes)
		if err == nil {
			data = fetched
			if strings.HasSuffix(strings.ToLower(entry.IconFile.Path), ".png") {
				mime = "image/png"
			}
		}
	case entry.Icon != "" && !strings.HasPrefix(entry.Icon, "text"):
		if fetched, err := installer.lobeIcon(ctx, entry.Icon); err == nil {
			data = fetched
		}
	}
	icon := ""
	if data != nil && (mime == "image/png" && bytes.HasPrefix(data, []byte("\x89PNG")) || mime == "image/svg+xml" && bytes.Contains(data, []byte("<svg"))) {
		icon = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	installer.market.mutex.Lock()
	if installer.market.icons == nil {
		installer.market.icons = map[string]string{}
	}
	installer.market.icons[cacheKey] = icon
	installer.market.mutex.Unlock()
	return icon
}
