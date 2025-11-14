package autoscaler

import "time"

// Metric представляет метрику нагрузки
type Metric struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
}

// Prediction представляет предсказание нагрузки
type Prediction struct {
	Timestamp      time.Time `json:"timestamp"`
	PredictedValue float64   `json:"predicted_value"`
	Confidence     float64   `json:"confidence"`
	Algorithm      string    `json:"algorithm"`
	ActualValue    float64   `json:"actual_value,omitempty"`
}

// ScaleEvent событие масштабирования
type ScaleEvent struct {
	Timestamp   time.Time `json:"timestamp"`
	OldReplicas int32     `json:"old_replicas"`
	NewReplicas int32     `json:"new_replicas"`
	Reason      string    `json:"reason"`
	MetricValue float64   `json:"metric_value"`
	Predicted   float64   `json:"predicted_value"`
	Confidence  float64   `json:"confidence"`
	Duration    float64   `json:"duration_ms"`
}

// AutoscalerConfig конфигурация автоскейлера
type AutoscalerConfig struct {
	ScaleUpThreshold          float64       `json:"scale_up_threshold"`
	ScaleDownThreshold        float64       `json:"scale_down_threshold"`
	MinReplicas               int32         `json:"min_replicas"`
	MaxReplicas               int32         `json:"max_replicas"`
	WindowSize                int           `json:"window_size"`
	PredictionInterval        int           `json:"prediction_interval"`
	CooldownPeriod            time.Duration `json:"cooldown_period"`
	StabilizationWindow       time.Duration `json:"stabilization_window"`
	MetricsCollectionInterval int           `json:"metrics_collection_interval"`
	PredictionAlgorithm       string        `json:"prediction_algorithm"`
}

// K8sConfig конфигурация Kubernetes
type K8sConfig struct {
	Enabled          bool   `json:"enabled"`
	Namespace        string `json:"namespace"`
	DeploymentName   string `json:"deployment_name"`
	KubeconfigPath   string `json:"kubeconfig_path"`
	MetricsServerURL string `json:"metrics_server_url"`
	ScaleAPIEnabled  bool   `json:"scale_api_enabled"`
	InCluster        bool   `json:"in_cluster"`
}

// CustomMetricConfig конфигурация кастомных метрик
type CustomMetricConfig struct {
	Name      string  `json:"name"`
	Query     string  `json:"query"`
	Weight    float64 `json:"weight"`
	Type      string  `json:"type"`
	Threshold float64 `json:"threshold"`
}
