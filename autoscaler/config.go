package autoscaler

import "time"

// DefaultConfig возвращает конфигурацию по умолчанию
func DefaultConfig() AutoscalerConfig {
	return AutoscalerConfig{
		ScaleUpThreshold:          75.0,
		ScaleDownThreshold:        25.0,
		MinReplicas:               1,
		MaxReplicas:               10,
		WindowSize:                100,
		PredictionInterval:        15,
		CooldownPeriod:            60 * time.Second,
		StabilizationWindow:       300 * time.Second,
		MetricsCollectionInterval: 5,
		PredictionAlgorithm:       "linear_regression",
	}
}

// DefaultK8sConfig возвращает конфигурацию Kubernetes по умолчанию
func DefaultK8sConfig() K8sConfig {
	return K8sConfig{
		Enabled:         false,
		Namespace:       "default",
		DeploymentName:  "demo-app",
		InCluster:       true,
		ScaleAPIEnabled: true,
	}
}

// WithScaleUpThreshold устанавливает порог масштабирования вверх
func (c AutoscalerConfig) WithScaleUpThreshold(threshold float64) AutoscalerConfig {
	c.ScaleUpThreshold = threshold
	return c
}

// WithScaleDownThreshold устанавливает порог масштабирования вниз
func (c AutoscalerConfig) WithScaleDownThreshold(threshold float64) AutoscalerConfig {
	c.ScaleDownThreshold = threshold
	return c
}

// WithReplicaRange устанавливает минимальное и максимальное количество реплик
func (c AutoscalerConfig) WithReplicaRange(min, max int32) AutoscalerConfig {
	c.MinReplicas = min
	c.MaxReplicas = max
	return c
}
