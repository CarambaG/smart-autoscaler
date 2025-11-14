package autoscaler

import (
	"context"
	"fmt"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/mem"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ===========================
// Сбор метрик
// ===========================

// CollectSystemMetrics собирает системные метрики
func (a *Autoscaler) CollectSystemMetrics() {
	go func() {
		ticker := time.NewTicker(time.Duration(a.config.MetricsCollectionInterval) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if !a.isRunning {
				return
			}

			// Сбор CPU метрик
			cpuPercentages, err := cpu.Percent(time.Second, false)
			if err == nil && len(cpuPercentages) > 0 {
				cpuMetric := Metric{
					Timestamp: time.Now(),
					Value:     cpuPercentages[0],
					Type:      "cpu_usage",
					Source:    "system",
				}
				a.AddMetric(cpuMetric)
				a.cpuUsageGauge.Set(cpuPercentages[0])
			}

			// Сбор memory метрик
			memInfo, err := mem.VirtualMemory()
			if err == nil {
				memoryMetric := Metric{
					Timestamp: time.Now(),
					Value:     memInfo.UsedPercent,
					Type:      "memory_usage",
					Source:    "system",
				}
				a.AddMetric(memoryMetric)
				a.memoryUsageGauge.Set(memInfo.UsedPercent)
			}

			// Сбор Kubernetes метрик
			if a.k8sConfig.Enabled {
				a.collectK8sMetrics()
			}
		}
	}()
}

// collectK8sMetrics собирает метрики из Kubernetes
func (a *Autoscaler) collectK8sMetrics() {
	// Сбор метрик Pod'ов через Metrics Server
	if a.metricsClient != nil {
		podMetricsList, err := a.metricsClient.PodMetricses(a.k8sConfig.Namespace).List(context.TODO(), metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", a.k8sConfig.DeploymentName),
		})

		if err == nil {
			totalCPU, totalMemory, podCount := a.calculatePodResourceUsage(podMetricsList.Items)

			if podCount > 0 {
				cpuMetric := Metric{
					Timestamp: time.Now(),
					Value:     totalCPU / float64(podCount),
					Type:      "k8s_pod_cpu",
					Source:    "kubernetes",
				}
				a.AddMetric(cpuMetric)

				memoryMetric := Metric{
					Timestamp: time.Now(),
					Value:     totalMemory / float64(podCount),
					Type:      "k8s_pod_memory",
					Source:    "kubernetes",
				}
				a.AddMetric(memoryMetric)
			}
		}
	}

	// Сбор информации о Deployment
	deployment, err := a.k8sClient.AppsV1().Deployments(a.k8sConfig.Namespace).
		Get(context.TODO(), a.k8sConfig.DeploymentName, metav1.GetOptions{})
	if err == nil {
		replicasMetric := Metric{
			Timestamp: time.Now(),
			Value:     float64(deployment.Status.AvailableReplicas),
			Type:      "k8s_available_replicas",
			Source:    "kubernetes",
		}
		a.AddMetric(replicasMetric)
		a.currentReplicasGauge.Set(float64(deployment.Status.AvailableReplicas))
	}
}

// AddMetric добавляет новую метрику
func (a *Autoscaler) AddMetric(metric Metric) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.metrics = append(a.metrics, metric)

	// Сохраняем только последние N метрик
	if len(a.metrics) > a.config.WindowSize {
		a.metrics = a.metrics[len(a.metrics)-a.config.WindowSize:]
	}
}
