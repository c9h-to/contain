package v1

type ContainConfig struct {
	Status ContainConfigStatus
	// Base is the base image reference
	Base string `yaml:"base"`
	// Tag is the result reference to be pushed
	Tag       string   `yaml:"tag"`
	Platforms []string `yaml:"platforms"`
	Layers    []Layer  `yaml:"layers,omitempty"`
}

type ContainConfigStatus struct {
	Template  bool   // true if config is from a template
	Md5       string // config source md5 (not for template)
	Sha256    string // config source sha256 (not for template)
	Overrides ContainConfigOverrides
}

type ContainConfigOverrides struct {
	Base bool
}

type Layer struct {
	// generic, supported for applicable layer types
	Uid  uint8
	Gid  uint8
	Mode // TODO
	// exactly one of the following
	LocalDir LocalDir `yaml:"localDir,omitempty"`
}

// LocalDir is a directory structure that should be appended as-is to base
// with an optional path prefix, for example ./target/app to /app
type LocalDir struct {
	Path          string   `yaml:"path"`
	ContainerPath string   `yaml:"containerPath,omitempty"`
	Ignore        []string `yaml:"ignore,omitempty"`
	MaxFiles      int      `yaml:"maxFiles,omitempty"`
	MaxSize       string   `yaml:"maxSize,omitempty"`
}
