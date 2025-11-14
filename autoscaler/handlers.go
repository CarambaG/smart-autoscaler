package autoscaler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// ===========================
// HTTP Handlers
// ===========================

// metricsHandler возвращает метрики
func (a *Autoscaler) metricsHandler(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.metrics)
}

// predictionsHandler возвращает предсказания
func (a *Autoscaler) predictionsHandler(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.predictions)
}

// statusHandler возвращает статус автоскейлера
func (a *Autoscaler) statusHandler(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	currentReplicas, _ := a.getCurrentReplicas()

	status := map[string]interface{}{
		"status":             "running",
		"uptime":             time.Since(a.startTime).String(),
		"current_replicas":   currentReplicas,
		"current_load":       a.getCurrentLoad(),
		"total_metrics":      len(a.metrics),
		"total_predictions":  len(a.predictions),
		"total_scale_events": len(a.scaleHistory),
		"config":             a.config,
		"k8s_config":         a.k8sConfig,
		"last_scale_time":    a.lastScaleTime,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(status)
}

// scaleHistoryHandler возвращает историю масштабирования
func (a *Autoscaler) scaleHistoryHandler(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(a.scaleHistory)
}

// healthHandler проверяет здоровье автоскейлера
func (a *Autoscaler) healthHandler(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now(),
		"version":   "1.0.0",
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(health)
}

// manualScaleHandler ручное масштабирование
func (a *Autoscaler) manualScaleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var request struct {
		Replicas int32  `json:"replicas"`
		Reason   string `json:"reason"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if request.Replicas < a.config.MinReplicas || request.Replicas > a.config.MaxReplicas {
		http.Error(w, "Replicas out of range", http.StatusBadRequest)
		return
	}

	currentReplicas, err := a.getCurrentReplicas()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	err = a.scaleDeployment(request.Replicas, "manual: "+request.Reason, 0, 0, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"status":   "success",
		"message":  fmt.Sprintf("Scaled from %d to %d replicas", currentReplicas, request.Replicas),
		"replicas": request.Replicas,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
