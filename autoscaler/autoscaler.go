package autoscaler

import (
	"context"
	"fmt"
	"log"
	"math"
	_ "math/rand"
	"net/http"
	_ "strconv"
	"sync"
	"time"

	_ "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	_ "k8s.io/api/core/v1"
	_ "k8s.io/apimachinery/pkg/api/errors"
	_ "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsv1beta1 "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ===========================
// Структуры данных
// ===========================

// Autoscaler основной класс автоскейлера
type Autoscaler struct {
	config             AutoscalerConfig
	k8sConfig          K8sConfig
	metrics            []Metric
	predictions        []Prediction
	scaleHistory       []ScaleEvent
	currentReplicas    int32
	mu                 sync.RWMutex
	k8sClient          *kubernetes.Clientset
	metricsClient      *metricsv1beta1.MetricsV1beta1Client
	lastScaleTime      time.Time
	startTime          time.Time
	isRunning          bool
	customMetrics      []CustomMetricConfig
	prometheusRegistry *prometheus.Registry

	// Prometheus метрики
	scaleEventsCounter   prometheus.Counter
	predictionErrorGauge prometheus.Gauge
	currentReplicasGauge prometheus.Gauge
	cpuUsageGauge        prometheus.Gauge
	memoryUsageGauge     prometheus.Gauge
}

// ===========================
// Инициализация
// ===========================

// NewAutoscaler создает новый автоскейлер
func NewAutoscaler(config AutoscalerConfig, k8sConfig K8sConfig) (*Autoscaler, error) {
	a := &Autoscaler{
		config:          config,
		k8sConfig:       k8sConfig,
		metrics:         make([]Metric, 0),
		predictions:     make([]Prediction, 0),
		scaleHistory:    make([]ScaleEvent, 0),
		currentReplicas: config.MinReplicas,
		startTime:       time.Now(),
		isRunning:       true,
		customMetrics:   make([]CustomMetricConfig, 0),
	}

	// Инициализация Prometheus метрик
	a.initPrometheusMetrics()

	// Инициализация Kubernetes клиента
	if k8sConfig.Enabled {
		if err := a.initK8sClient(); err != nil {
			return nil, fmt.Errorf("failed to initialize Kubernetes client: %v", err)
		}

		// Получаем текущее количество реплик
		if currentReplicas, err := a.getCurrentReplicas(); err == nil {
			a.currentReplicas = currentReplicas
		} else {
			log.Printf("Warning: Could not get current replicas: %v", err)
		}
	}

	return a, nil
}

// initPrometheusMetrics инициализирует Prometheus метрики
func (a *Autoscaler) initPrometheusMetrics() {
	a.prometheusRegistry = prometheus.NewRegistry()

	a.scaleEventsCounter = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "autoscaler_scale_events_total",
		Help: "Total number of scale events",
	})

	a.predictionErrorGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "autoscaler_prediction_error",
		Help: "Prediction error percentage",
	})

	a.currentReplicasGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "autoscaler_current_replicas",
		Help: "Current number of replicas",
	})

	a.cpuUsageGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "autoscaler_cpu_usage_percent",
		Help: "Current CPU usage percentage",
	})

	a.memoryUsageGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "autoscaler_memory_usage_percent",
		Help: "Current memory usage percentage",
	})

	// Регистрируем метрики
	a.prometheusRegistry.MustRegister(
		a.scaleEventsCounter,
		a.predictionErrorGauge,
		a.currentReplicasGauge,
		a.cpuUsageGauge,
		a.memoryUsageGauge,
	)
}

// initK8sClient инициализирует клиент Kubernetes
func (a *Autoscaler) initK8sClient() error {
	var config *rest.Config
	var err error

	if a.k8sConfig.KubeconfigPath != "" && !a.k8sConfig.InCluster {
		config, err = clientcmd.BuildConfigFromFlags("", a.k8sConfig.KubeconfigPath)
	} else {
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return err
	}

	// Создаем клиент для основных операций
	a.k8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}

	// Создаем клиент для метрик (опционально)
	a.metricsClient, err = metricsv1beta1.NewForConfig(config)
	if err != nil {
		log.Printf("Warning: Metrics server client not available: %v", err)
	}

	return nil
}

// calculatePodResourceUsage вычисляет использование ресурсов Pod'ами
func (a *Autoscaler) calculatePodResourceUsage(podMetrics []v1beta1.PodMetrics) (float64, float64, int) {
	var totalCPU, totalMemory float64
	podCount := 0

	for _, podMetric := range podMetrics {
		for _, container := range podMetric.Containers {
			// CPU usage (конвертируем из nancores в милликоры)
			if !container.Usage.Cpu().IsZero() {
				cpuUsage := container.Usage.Cpu().MilliValue()
				totalCPU += float64(cpuUsage) / 10.0 // Конвертируем в проценты
			}

			// Memory usage (конвертируем в проценты)
			if !container.Usage.Memory().IsZero() {
				memoryUsage := container.Usage.Memory().Value()
				// Предполагаем, что каждый Pod имеет лимит 512MB для демонстрации
				memoryPercent := (float64(memoryUsage) / (512 * 1024 * 1024)) * 100
				totalMemory += memoryPercent
			}
		}
		podCount++
	}

	return totalCPU, totalMemory, podCount
}

// ===========================
// Алгоритмы предсказания
// ===========================

// PredictLoad предсказывает нагрузку
func (a *Autoscaler) PredictLoad() Prediction {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.metrics) < 5 {
		return Prediction{
			Timestamp:      time.Now(),
			PredictedValue: a.getCurrentLoad(),
			Confidence:     0.5,
			Algorithm:      "fallback",
		}
	}

	switch a.config.PredictionAlgorithm {
	case "linear_regression":
		return a.predictLinearRegression()
	case "moving_average":
		return a.predictMovingAverage()
	case "exponential_smoothing":
		return a.predictExponentialSmoothing()
	default:
		return a.predictLinearRegression()
	}
}

// predictLinearRegression предсказывает с помощью линейной регрессии
func (a *Autoscaler) predictLinearRegression() Prediction {
	var sumX, sumY, sumXY, sumXX float64
	n := float64(len(a.metrics))

	// Берем только последние N метрик для предсказания
	recentMetrics := a.metrics
	if len(recentMetrics) > 20 {
		recentMetrics = recentMetrics[len(recentMetrics)-20:]
		n = 20
	}

	for i, metric := range recentMetrics {
		x := float64(i)
		y := metric.Value
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}

	slope := (n*sumXY - sumX*sumY) / (n*sumXX - sumX*sumX)
	intercept := (sumY - slope*sumX) / n

	// Предсказываем следующее значение
	nextX := float64(len(recentMetrics))
	predicted := slope*nextX + intercept

	// Расчет уверенности (R-squared)
	var ssTotal, ssResidual float64
	meanY := sumY / n

	for i, metric := range recentMetrics {
		x := float64(i)
		y := metric.Value
		predictedY := slope*x + intercept
		ssTotal += math.Pow(y-meanY, 2)
		ssResidual += math.Pow(y-predictedY, 2)
	}

	rSquared := 1.0 - (ssResidual / ssTotal)
	if rSquared < 0 {
		rSquared = 0
	}

	return Prediction{
		Timestamp:      time.Now().Add(time.Duration(a.config.PredictionInterval) * time.Second),
		PredictedValue: math.Max(0, predicted), // Не допускаем отрицательные значения
		Confidence:     math.Min(1.0, rSquared),
		Algorithm:      "linear_regression",
	}
}

// predictMovingAverage предсказывает с помощью скользящего среднего
func (a *Autoscaler) predictMovingAverage() Prediction {
	window := 5
	if len(a.metrics) < window {
		window = len(a.metrics)
	}

	var sum float64
	for i := len(a.metrics) - window; i < len(a.metrics); i++ {
		sum += a.metrics[i].Value
	}

	predicted := sum / float64(window)

	return Prediction{
		Timestamp:      time.Now().Add(time.Duration(a.config.PredictionInterval) * time.Second),
		PredictedValue: predicted,
		Confidence:     0.7, // Фиксированная уверенность для простого алгоритма
		Algorithm:      "moving_average",
	}
}

// predictExponentialSmoothing предсказывает с помощью экспоненциального сглаживания
func (a *Autoscaler) predictExponentialSmoothing() Prediction {
	if len(a.metrics) == 0 {
		return Prediction{
			Timestamp:      time.Now(),
			PredictedValue: 0,
			Confidence:     0.5,
			Algorithm:      "exponential_smoothing",
		}
	}

	alpha := 0.3 // Коэффициент сглаживания
	predicted := a.metrics[0].Value

	for i := 1; i < len(a.metrics); i++ {
		predicted = alpha*a.metrics[i].Value + (1-alpha)*predicted
	}

	return Prediction{
		Timestamp:      time.Now().Add(time.Duration(a.config.PredictionInterval) * time.Second),
		PredictedValue: predicted,
		Confidence:     0.6,
		Algorithm:      "exponential_smoothing",
	}
}

// ===========================
// Kubernetes операции
// ===========================

// getCurrentReplicas получает текущее количество реплик
func (a *Autoscaler) getCurrentReplicas() (int32, error) {
	if a.k8sClient == nil {
		return a.currentReplicas, nil
	}

	deployment, err := a.k8sClient.AppsV1().Deployments(a.k8sConfig.Namespace).
		Get(context.TODO(), a.k8sConfig.DeploymentName, metav1.GetOptions{})
	if err != nil {
		return 0, err
	}

	if deployment.Spec.Replicas == nil {
		return 1, nil
	}

	return *deployment.Spec.Replicas, nil
}

// scaleDeployment масштабирует Deployment
func (a *Autoscaler) scaleDeployment(newReplicas int32, reason string, metricValue, predictedValue, confidence float64) error {
	startTime := time.Now()

	if a.k8sClient == nil {
		a.currentReplicas = newReplicas
		log.Printf("Simulated scale to %d replicas: %s", newReplicas, reason)
		return nil
	}

	scale := &autoscalingv1.Scale{
		ObjectMeta: metav1.ObjectMeta{
			Name:      a.k8sConfig.DeploymentName,
			Namespace: a.k8sConfig.Namespace,
		},
		Spec: autoscalingv1.ScaleSpec{
			Replicas: newReplicas,
		},
	}

	_, err := a.k8sClient.AppsV1().Deployments(a.k8sConfig.Namespace).
		UpdateScale(context.TODO(), a.k8sConfig.DeploymentName, scale, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	// Записываем событие масштабирования
	duration := time.Since(startTime).Seconds() * 1000
	oldReplicas := a.currentReplicas
	a.currentReplicas = newReplicas
	a.recordScaleEvent(oldReplicas, newReplicas, reason, metricValue, predictedValue, confidence, duration)

	// Обновляем Prometheus метрики
	a.scaleEventsCounter.Inc()
	a.currentReplicasGauge.Set(float64(newReplicas))

	log.Printf("Successfully scaled deployment %s/%s from %d to %d replicas in %.2fms: %s",
		a.k8sConfig.Namespace, a.k8sConfig.DeploymentName,
		oldReplicas, newReplicas, duration, reason)

	return nil
}

// recordScaleEvent записывает событие масштабирования
func (a *Autoscaler) recordScaleEvent(oldReplicas, newReplicas int32, reason string, metricValue, predictedValue, confidence, duration float64) {
	event := ScaleEvent{
		Timestamp:   time.Now(),
		OldReplicas: oldReplicas,
		NewReplicas: newReplicas,
		Reason:      reason,
		MetricValue: metricValue,
		Predicted:   predictedValue,
		Confidence:  confidence,
		Duration:    duration,
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	a.scaleHistory = append(a.scaleHistory, event)
	if len(a.scaleHistory) > 100 {
		a.scaleHistory = a.scaleHistory[len(a.scaleHistory)-100:]
	}
}

// canScale проверяет можно ли выполнять масштабирование
func (a *Autoscaler) canScale() bool {
	return time.Since(a.lastScaleTime) >= a.config.CooldownPeriod
}

// ===========================
// Логика принятия решений
// ===========================

// MakeScalingDecision принимает решение о масштабировании
func (a *Autoscaler) MakeScalingDecision() string {
	if !a.canScale() {
		remaining := a.config.CooldownPeriod - time.Since(a.lastScaleTime)
		return fmt.Sprintf("COOLDOWN: Waiting %.1f seconds", remaining.Seconds())
	}

	currentLoad := a.getCurrentLoad()
	prediction := a.PredictLoad()

	currentReplicas, err := a.getCurrentReplicas()
	if err != nil {
		log.Printf("Error getting current replicas: %v", err)
		return "ERROR: Could not get current replicas"
	}

	var decision string
	var newReplicas int32

	// Улучшенная логика с учетом предсказания и уверенности
	if prediction.PredictedValue > a.config.ScaleUpThreshold &&
		prediction.Confidence > 0.6 &&
		currentReplicas < a.config.MaxReplicas {

		scaleFactor := a.calculateScaleFactor(prediction.PredictedValue, currentReplicas)
		newReplicas = currentReplicas + scaleFactor
		if newReplicas > a.config.MaxReplicas {
			newReplicas = a.config.MaxReplicas
		}

		decision = fmt.Sprintf("SCALE_UP from %d to %d (current: %.2f, predicted: %.2f, confidence: %.2f)",
			currentReplicas, newReplicas, currentLoad, prediction.PredictedValue, prediction.Confidence)

	} else if prediction.PredictedValue < a.config.ScaleDownThreshold &&
		prediction.Confidence > 0.7 &&
		currentReplicas > a.config.MinReplicas {

		scaleFactor := a.calculateScaleDownFactor(prediction.PredictedValue, currentReplicas)
		newReplicas = currentReplicas - scaleFactor
		if newReplicas < a.config.MinReplicas {
			newReplicas = a.config.MinReplicas
		}

		decision = fmt.Sprintf("SCALE_DOWN from %d to %d (current: %.2f, predicted: %.2f, confidence: %.2f)",
			currentReplicas, newReplicas, currentLoad, prediction.PredictedValue, prediction.Confidence)

	} else {
		decision = fmt.Sprintf("MAINTAIN at %d (current: %.2f, predicted: %.2f, confidence: %.2f)",
			currentReplicas, currentLoad, prediction.PredictedValue, prediction.Confidence)
		return decision
	}

	// Выполняем масштабирование
	if newReplicas != currentReplicas {
		err := a.scaleDeployment(newReplicas, decision, currentLoad, prediction.PredictedValue, prediction.Confidence)
		if err != nil {
			decision = fmt.Sprintf("SCALE_ERROR: %v", err)
		} else {
			a.lastScaleTime = time.Now()

			// Сохраняем предсказание с фактическим значением
			a.mu.Lock()
			prediction.ActualValue = currentLoad
			a.predictions = append(a.predictions, prediction)
			if len(a.predictions) > 50 {
				a.predictions = a.predictions[len(a.predictions)-50:]
			}
			a.mu.Unlock()
		}
	}

	log.Printf("Decision: %s", decision)
	return decision
}

// calculateScaleFactor вычисляет коэффициент масштабирования
func (a *Autoscaler) calculateScaleFactor(predictedLoad float64, currentReplicas int32) int32 {
	excess := (predictedLoad - a.config.ScaleUpThreshold) / a.config.ScaleUpThreshold
	scaleFactor := int32(math.Ceil(float64(currentReplicas) * excess))

	if scaleFactor < 1 {
		scaleFactor = 1
	}
	if scaleFactor > 5 {
		scaleFactor = 5
	}

	return scaleFactor
}

// calculateScaleDownFactor вычисляет коэффициент уменьшения
func (a *Autoscaler) calculateScaleDownFactor(predictedLoad float64, currentReplicas int32) int32 {
	deficit := (a.config.ScaleDownThreshold - predictedLoad) / a.config.ScaleDownThreshold
	scaleFactor := int32(math.Ceil(float64(currentReplicas) * deficit * 0.3)) // 30% от расчетного

	if scaleFactor < 1 {
		scaleFactor = 1
	}
	if scaleFactor > 3 {
		scaleFactor = 3
	}

	return scaleFactor
}

// getCurrentLoad возвращает текущую нагрузку
func (a *Autoscaler) getCurrentLoad() float64 {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if len(a.metrics) == 0 {
		return 0.0
	}

	// Берем среднее значение последних 3 метрик CPU для стабильности
	sum := 0.0
	count := 0
	for i := len(a.metrics) - 1; i >= 0 && i >= len(a.metrics)-3; i-- {
		if a.metrics[i].Type == "cpu_usage" || a.metrics[i].Type == "k8s_pod_cpu" {
			sum += a.metrics[i].Value
			count++
		}
	}

	if count > 0 {
		return sum / float64(count)
	}
	return a.metrics[len(a.metrics)-1].Value
}

// ===========================
// Основная логика
// ===========================

// Start начинает работу автоскейлера
func (a *Autoscaler) Start() {
	log.Println("Starting Smart Autoscaler...")

	// Запускаем сбор метрик
	a.CollectSystemMetrics()

	// Основной цикл принятия решений
	go func() {
		ticker := time.NewTicker(time.Duration(a.config.PredictionInterval) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if !a.isRunning {
				return
			}
			a.MakeScalingDecision()
		}
	}()
}

// Stop останавливает автоскейлер
func (a *Autoscaler) Stop() {
	log.Println("Stopping Smart Autoscaler...")
	a.isRunning = false
}

// SetupRouter настраивает HTTP роутер
func (a *Autoscaler) SetupRouter() *mux.Router {
	r := mux.NewRouter()

	// API endpoints
	r.HandleFunc("/api/health", a.healthHandler).Methods("GET")
	r.HandleFunc("/api/status", a.statusHandler).Methods("GET")
	r.HandleFunc("/api/metrics", a.metricsHandler).Methods("GET")
	r.HandleFunc("/api/predictions", a.predictionsHandler).Methods("GET")
	r.HandleFunc("/api/scale-history", a.scaleHistoryHandler).Methods("GET")
	r.HandleFunc("/api/scale", a.manualScaleHandler).Methods("POST")

	// Prometheus metrics
	r.Handle("/metrics", promhttp.HandlerFor(a.prometheusRegistry, promhttp.HandlerOpts{}))

	// Web UI
	r.PathPrefix("/").Handler(http.FileServer(http.Dir("./static/")))

	return r
}
