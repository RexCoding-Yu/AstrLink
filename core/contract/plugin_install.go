package contract

// Plugins installed from source (plugin platform v2, §7): a GitHub link is
// resolved to a commit, its source built in Core with the dependencies its
// lock file pins, reviewed, and installed only once the user confirms.

// Where an installed plugin's source came from.
const (
	PluginRefTag    = "tag"
	PluginRefBranch = "branch"
	PluginRefCommit = "commit"
)

// PluginInstallSource is where a plugin installed from source came from.
type PluginInstallSource struct {
	// Kind is "github", the only kind so far.
	Kind string `json:"kind"`
	// Repository is owner/name.
	Repository string `json:"repository"`
	// URL is the link the install started from.
	URL string `json:"url"`
	// Ref is the tag or branch installed, "" for a commit.
	Ref string `json:"ref,omitempty"`
	// RefKind is tag, branch or commit: what an update follows.
	RefKind string `json:"ref_kind"`
	// Commit is the commit built.
	Commit      string `json:"commit"`
	CommittedAt string `json:"committed_at,omitempty"`
	// Subdir is the plugin's directory in the repository, "" for its root.
	Subdir string `json:"subdir,omitempty"`
}

// PluginInstallRecord is install.json beside an installed plugin: where its
// files came from and what went into them.
type PluginInstallRecord struct {
	Source  PluginInstallSource `json:"source"`
	Version string              `json:"version"`
	// Lockfile is the lock file the dependencies came from, "" for none,
	// and LockfileSHA256 its hash.
	Lockfile       string `json:"lockfile,omitempty"`
	LockfileSHA256 string `json:"lockfile_sha256,omitempty"`
	// Outputs are the sha256 of each file built.
	Outputs      map[string]string  `json:"outputs"`
	Dependencies []PluginDependency `json:"dependencies,omitempty"`
	InstalledAt  string             `json:"installed_at"`
}

// PluginDependency is a package a build took from the lock file.
type PluginDependency struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	// Bytes is its unpacked size.
	Bytes int64 `json:"bytes"`
	// Scripts is set for a package with install scripts, which never ran.
	Scripts bool `json:"scripts,omitempty"`
}

// PluginInstallPreviewRequest asks for a plugin to be fetched, built and
// reviewed.
type PluginInstallPreviewRequest struct {
	URL string `json:"url"`
}

// PluginInstallRequest installs what a review staged.
type PluginInstallRequest struct {
	Token string `json:"token"`
}

// PluginRollbackRequest puts back a version a later install replaced.
type PluginRollbackRequest struct {
	Version string `json:"version"`
}

// PluginInstallReview is what the user reviews before a plugin is
// installed: where it comes from, what it asks for, and what it replaces.
type PluginInstallReview struct {
	// Token names the build staged for the install, until ExpiresAt.
	Token     string              `json:"token"`
	ExpiresAt string              `json:"expires_at"`
	Source    PluginInstallSource `json:"source"`

	Key            string   `json:"key"`
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Description    string   `json:"description,omitempty"`
	Author         string   `json:"author,omitempty"`
	APIVersion     int      `json:"api_version"`
	Capabilities   []string `json:"capabilities"`
	NetworkPurpose string   `json:"network_purpose,omitempty"`
	Hooks          []string `json:"hooks"`
	// Client is set when the plugin brings interface code.
	Client      bool `json:"client,omitempty"`
	PluginBytes int  `json:"plugin_bytes"`
	ClientBytes int  `json:"client_bytes,omitempty"`

	Lockfile        string             `json:"lockfile,omitempty"`
	Dependencies    []PluginDependency `json:"dependencies"`
	DependencyBytes int64              `json:"dependency_bytes"`
	// Engines is the range of AstrLink versions the plugin declares.
	Engines  string                 `json:"engines,omitempty"`
	Warnings []PluginInstallWarning `json:"warnings,omitempty"`

	// Replaces is the plugin of the same key the install replaces, and
	// Changes what differs from it.
	Replaces *PluginInstallReplace `json:"replaces,omitempty"`
	Changes  *PluginInstallChanges `json:"changes,omitempty"`
}

// Warnings of a review.
const (
	// PluginWarningScripts: a package's install scripts did not run.
	PluginWarningScripts = "install_scripts_skipped"
	// PluginWarningVersion: package.json and meta.version disagree.
	PluginWarningVersion = "version_mismatch"
	// PluginWarningBuild: the bundler warned.
	PluginWarningBuild = "build_warning"
)

// PluginInstallWarning is something the review points out that does not
// stop the install.
type PluginInstallWarning struct {
	Code    string `json:"code"`
	Package string `json:"package,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// What an install replaces.
const (
	PluginReplacesBuiltin     = "builtin"
	PluginReplacesSource      = "source"
	PluginReplacesDevelopment = "development"
	PluginReplacesDirectory   = "directory"
)

// PluginInstallReplace is the plugin an install replaces.
type PluginInstallReplace struct {
	// Origin is builtin, source, development or directory.
	Origin  string `json:"origin"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	// Repository is set for a plugin installed from another repository.
	Repository string `json:"repository,omitempty"`
}

// PluginInstallChanges is what an install changes of the plugin it
// replaces.
type PluginInstallChanges struct {
	FromVersion         string   `json:"from_version,omitempty"`
	AddedCapabilities   []string `json:"added_capabilities,omitempty"`
	RemovedCapabilities []string `json:"removed_capabilities,omitempty"`
	// Client is "added" or "removed" when the interface code comes or
	// goes.
	Client                string   `json:"client,omitempty"`
	NetworkPurposeChanged bool     `json:"network_purpose_changed,omitempty"`
	AddedDependencies     []string `json:"added_dependencies,omitempty"`
	RemovedDependencies   []string `json:"removed_dependencies,omitempty"`
	// ChangedDependencies read "name 1.0.0 → 1.1.0".
	ChangedDependencies []string `json:"changed_dependencies,omitempty"`
}

// Why an install could not be reviewed.
const (
	PluginInstallInvalidLink           = "invalid_link"
	PluginInstallRepositoryNotFound    = "repository_not_found"
	PluginInstallRefNotFound           = "ref_not_found"
	PluginInstallRateLimited           = "rate_limited"
	PluginInstallUnavailable           = "source_unavailable"
	PluginInstallTooLarge              = "too_large"
	PluginInstallNoPlugin              = "no_plugin"
	PluginInstallNoLockfile            = "no_lockfile"
	PluginInstallLockfileUnsupported   = "lockfile_unsupported"
	PluginInstallDependencyUnsupported = "dependency_unsupported"
	PluginInstallIntegrity             = "integrity_failed"
	PluginInstallRegistryUnavailable   = "registry_unavailable"
	PluginInstallNativeModule          = "native_module"
	PluginInstallBuildFailed           = "build_failed"
	PluginInstallInvalidPlugin         = "invalid_plugin"
	PluginInstallIncompatible          = "incompatible"
	PluginInstallExpired               = "expired"
	PluginInstallBusy                  = "busy"
)

// PluginInstallFailure says why a plugin could not be fetched, built or
// installed, for the review to show instead of installing.
type PluginInstallFailure struct {
	Reason string `json:"reason"`
	// Detail names what failed: a package, a version range, a ref.
	Detail string `json:"detail,omitempty"`
	// Messages are the bundler's or the check's, for build_failed and
	// invalid_plugin.
	Messages []string `json:"messages,omitempty"`
}

// PluginUpdateCheck is what checking an installed plugin's repository for
// a newer version found.
type PluginUpdateCheck struct {
	// Pinned is set for a plugin installed from a commit, which has no
	// newer version to follow.
	Pinned    bool `json:"pinned,omitempty"`
	Available bool `json:"available"`
	// Ref is the newer tag, or the branch followed; Version the tag's
	// version.
	Ref         string `json:"ref,omitempty"`
	RefKind     string `json:"ref_kind,omitempty"`
	Commit      string `json:"commit,omitempty"`
	CommittedAt string `json:"committed_at,omitempty"`
	Version     string `json:"version,omitempty"`
	// Notes are the release's notes, or the new commits' first lines.
	Notes string `json:"notes,omitempty"`
	// URL is the link to review the update with.
	URL string `json:"url,omitempty"`
}

// PluginInstalledVersion is a version a later install replaced, kept to go
// back to.
type PluginInstalledVersion struct {
	ID      string               `json:"id"`
	Version string               `json:"version,omitempty"`
	Source  *PluginInstallSource `json:"source,omitempty"`
	// ReplacedAt is when a later install replaced it.
	ReplacedAt string `json:"replaced_at"`
}
