package config

// AppSpec represents the complete declarative application intent defined in RFC 001.
type AppSpec struct {
	Version      string                 `yaml:"version" json:"version"`
	Name         string                 `yaml:"name" json:"name"`
	Team         string                 `yaml:"team,omitempty" json:"team,omitempty"`
	Environment  string                 `yaml:"environment,omitempty" json:"environment,omitempty"`
	Type         string                 `yaml:"type" json:"type"`
	Build        BuildConfig            `yaml:"build,omitempty" json:"build,omitempty"`
	Image        string                 `yaml:"image,omitempty" json:"image,omitempty"`
	Runtime      RuntimeConfig          `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	Endpoints    []EndpointConfig       `yaml:"endpoints,omitempty" json:"endpoints,omitempty"`
	Health       HealthConfig           `yaml:"health,omitempty" json:"health,omitempty"`
	Security     SecurityConfig         `yaml:"security,omitempty" json:"security,omitempty"`
	Env          []EnvVar               `yaml:"env,omitempty" json:"env,omitempty"`
	Dependencies DependenciesConfig     `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	Patches      map[string]interface{} `yaml:"patches,omitempty" json:"patches,omitempty"`
}

type BuildConfig struct {
	Context    string            `yaml:"context,omitempty" json:"context,omitempty"`
	Builder    string            `yaml:"builder,omitempty" json:"builder,omitempty"`
	Dockerfile string            `yaml:"dockerfile,omitempty" json:"dockerfile,omitempty"`
	Args       map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

type RuntimeConfig struct {
	Replicas  ReplicaConfig  `yaml:"replicas,omitempty" json:"replicas,omitempty"`
	Resources ResourceConfig `yaml:"resources,omitempty" json:"resources,omitempty"`
}

type ReplicaConfig struct {
	Min                  int `yaml:"min,omitempty" json:"min,omitempty"`
	Max                  int `yaml:"max,omitempty" json:"max,omitempty"`
	TargetCPUUtilization int `yaml:"targetCPUUtilization,omitempty" json:"targetCPUUtilization,omitempty"`
}

type ResourceConfig struct {
	CPU    ResourceLimit `yaml:"cpu,omitempty" json:"cpu,omitempty"`
	Memory ResourceLimit `yaml:"memory,omitempty" json:"memory,omitempty"`
}

type ResourceLimit struct {
	Request string `yaml:"request,omitempty" json:"request,omitempty"`
	Limit   string `yaml:"limit,omitempty" json:"limit,omitempty"`
}

type EndpointConfig struct {
	Name      string           `yaml:"name" json:"name"`
	Port      int              `yaml:"port" json:"port"`
	Protocol  string           `yaml:"protocol,omitempty" json:"protocol,omitempty"`
	Public    bool             `yaml:"public,omitempty" json:"public,omitempty"`
	Domain    string           `yaml:"domain,omitempty" json:"domain,omitempty"`
	Path      string           `yaml:"path,omitempty" json:"path,omitempty"`
	RateLimit *RateLimitConfig `yaml:"rateLimit,omitempty" json:"rateLimit,omitempty"`
}

type RateLimitConfig struct {
	RequestsPerSecond int `yaml:"requestsPerSecond,omitempty" json:"requestsPerSecond,omitempty"`
}

type HealthConfig struct {
	Readiness *ProbeConfig `yaml:"readiness,omitempty" json:"readiness,omitempty"`
	Liveness  *ProbeConfig `yaml:"liveness,omitempty" json:"liveness,omitempty"`
	Startup   *ProbeConfig `yaml:"startup,omitempty" json:"startup,omitempty"`
}

type ProbeConfig struct {
	Path                string `yaml:"path,omitempty" json:"path,omitempty"`
	Port                int    `yaml:"port,omitempty" json:"port,omitempty"`
	InitialDelaySeconds int    `yaml:"initialDelaySeconds,omitempty" json:"initialDelaySeconds,omitempty"`
	PeriodSeconds       int    `yaml:"periodSeconds,omitempty" json:"periodSeconds,omitempty"`
	TimeoutSeconds      int    `yaml:"timeoutSeconds,omitempty" json:"timeoutSeconds,omitempty"`
	FailureThreshold    int    `yaml:"failureThreshold,omitempty" json:"failureThreshold,omitempty"`
}

type SecurityConfig struct {
	RunAsNonRoot             bool          `yaml:"runAsNonRoot,omitempty" json:"runAsNonRoot,omitempty"`
	ReadOnlyRootFilesystem   bool          `yaml:"readOnlyRootFilesystem,omitempty" json:"readOnlyRootFilesystem,omitempty"`
	AllowPrivilegeEscalation bool          `yaml:"allowPrivilegeEscalation,omitempty" json:"allowPrivilegeEscalation,omitempty"`
	Capabilities             *Capabilities `yaml:"capabilities,omitempty" json:"capabilities,omitempty"`
}

type Capabilities struct {
	Drop []string `yaml:"drop,omitempty" json:"drop,omitempty"`
	Add  []string `yaml:"add,omitempty" json:"add,omitempty"`
}

type EnvVar struct {
	Name          string `yaml:"name" json:"name"`
	Value         string `yaml:"value,omitempty" json:"value,omitempty"`
	FromSecret    string `yaml:"fromSecret,omitempty" json:"fromSecret,omitempty"`
	FromConfigMap string `yaml:"fromConfigMap,omitempty" json:"fromConfigMap,omitempty"`
}

type DependenciesConfig struct {
	Services []ServiceDependency `yaml:"services,omitempty" json:"services,omitempty"`
	Storage  []StorageDependency `yaml:"storage,omitempty" json:"storage,omitempty"`
}

type ServiceDependency struct {
	Name      string            `yaml:"name" json:"name"`
	Namespace string            `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	Required  bool              `yaml:"required,omitempty" json:"required,omitempty"`
	Type      string            `yaml:"type,omitempty" json:"type,omitempty"`
	Image     string            `yaml:"image,omitempty" json:"image,omitempty"`
	Port      int               `yaml:"port,omitempty" json:"port,omitempty"`
	Env       map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type StorageDependency struct {
	Name        string `yaml:"name" json:"name"`
	MountPath   string `yaml:"mountPath" json:"mountPath"`
	Size        string `yaml:"size,omitempty" json:"size,omitempty"`
	AccessMode  string `yaml:"accessMode,omitempty" json:"accessMode,omitempty"`
	Persistence string `yaml:"persistence,omitempty" json:"persistence,omitempty"`
}
