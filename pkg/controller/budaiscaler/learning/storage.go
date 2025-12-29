/*
Copyright 2024 Bud Studio.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package learning

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// ConfigMap data keys
	SeasonalProfileKey = "seasonal_profiles.json"
	PatternDetectorKey = "pattern_detector.json"
	AccuracyTrackerKey = "accuracy_tracker.json"
	MetadataKey        = "metadata.json"

	// Labels
	LearningDataLabel = "scaler.bud.studio/learning-data"
	ScalerNameLabel   = "scaler.bud.studio/scaler-name"

	// Annotations
	LastUpdatedAnnotation = "scaler.bud.studio/last-updated"
	VersionAnnotation     = "scaler.bud.studio/version"
)

// StorageMetadata contains metadata about the stored learning data
type StorageMetadata struct {
	Version          string    `json:"version"`
	ScalerName       string    `json:"scalerName"`
	Namespace        string    `json:"namespace"`
	CreatedAt        time.Time `json:"createdAt"`
	LastUpdated      time.Time `json:"lastUpdated"`
	DataPointCount   int       `json:"dataPointCount"`
	OldestDataPoint  time.Time `json:"oldestDataPoint"`
	ProfilesCount    int       `json:"profilesCount"`
	StorageSizeBytes int       `json:"storageSizeBytes"`
}

// LearningStorage handles persistence of learning data to ConfigMaps
type LearningStorage struct {
	client        client.Client
	configMapName string
	namespace     string
	ownerRef      *metav1.OwnerReference
}

// NewLearningStorage creates a new learning storage instance
func NewLearningStorage(c client.Client, scalerName, namespace string, ownerRef *metav1.OwnerReference) *LearningStorage {
	return &LearningStorage{
		client:        c,
		configMapName: fmt.Sprintf("%s-learning-data", scalerName),
		namespace:     namespace,
		ownerRef:      ownerRef,
	}
}

// LearningData represents all data to be persisted
type LearningData struct {
	SeasonalProfiles *SeasonalProfileManager    `json:"seasonalProfiles"`
	PatternDetector  *LLMPatternDetector        `json:"patternDetector"`
	AccuracyTracker  *PredictionAccuracyTracker `json:"accuracyTracker"`
	Metadata         StorageMetadata            `json:"metadata"`
}

// Save persists learning data to a ConfigMap
func (s *LearningStorage) Save(ctx context.Context, data *LearningData) error {
	// Update metadata
	data.Metadata.LastUpdated = time.Now()
	data.Metadata.Version = "v1"
	data.Metadata.Namespace = s.namespace

	// Serialize data
	seasonalData, err := json.Marshal(data.SeasonalProfiles)
	if err != nil {
		return fmt.Errorf("failed to marshal seasonal profiles: %w", err)
	}

	patternData, err := json.Marshal(data.PatternDetector)
	if err != nil {
		return fmt.Errorf("failed to marshal pattern detector: %w", err)
	}

	accuracyData, err := json.Marshal(data.AccuracyTracker)
	if err != nil {
		return fmt.Errorf("failed to marshal accuracy tracker: %w", err)
	}

	metadataData, err := json.Marshal(data.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Calculate total size
	totalSize := len(seasonalData) + len(patternData) + len(accuracyData) + len(metadataData)
	data.Metadata.StorageSizeBytes = totalSize

	// Create or update ConfigMap
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.configMapName,
			Namespace: s.namespace,
			Labels: map[string]string{
				LearningDataLabel: "true",
				ScalerNameLabel:   data.Metadata.ScalerName,
			},
			Annotations: map[string]string{
				LastUpdatedAnnotation: data.Metadata.LastUpdated.Format(time.RFC3339),
				VersionAnnotation:     data.Metadata.Version,
			},
		},
		Data: map[string]string{
			SeasonalProfileKey: string(seasonalData),
			PatternDetectorKey: string(patternData),
			AccuracyTrackerKey: string(accuracyData),
			MetadataKey:        string(metadataData),
		},
	}

	// Set owner reference for automatic cleanup
	if s.ownerRef != nil {
		cm.OwnerReferences = []metav1.OwnerReference{*s.ownerRef}
	}

	// Try to get existing ConfigMap
	existing := &corev1.ConfigMap{}
	err = s.client.Get(ctx, types.NamespacedName{
		Name:      s.configMapName,
		Namespace: s.namespace,
	}, existing)

	if errors.IsNotFound(err) {
		// Create new ConfigMap
		return s.client.Create(ctx, cm)
	} else if err != nil {
		return fmt.Errorf("failed to get existing ConfigMap: %w", err)
	}

	// Update existing ConfigMap
	existing.Data = cm.Data
	existing.Labels = cm.Labels
	existing.Annotations = cm.Annotations
	if s.ownerRef != nil && len(existing.OwnerReferences) == 0 {
		existing.OwnerReferences = []metav1.OwnerReference{*s.ownerRef}
	}

	return s.client.Update(ctx, existing)
}

// Load retrieves learning data from a ConfigMap
func (s *LearningStorage) Load(ctx context.Context) (*LearningData, error) {
	cm := &corev1.ConfigMap{}
	err := s.client.Get(ctx, types.NamespacedName{
		Name:      s.configMapName,
		Namespace: s.namespace,
	}, cm)

	if errors.IsNotFound(err) {
		return nil, nil // No data exists yet
	} else if err != nil {
		return nil, fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	data := &LearningData{
		SeasonalProfiles: NewSeasonalProfileManager(DefaultAlpha),
		PatternDetector:  NewLLMPatternDetector(),
		AccuracyTracker:  NewPredictionAccuracyTracker(),
	}

	// Deserialize seasonal profiles
	if seasonalJSON, ok := cm.Data[SeasonalProfileKey]; ok && seasonalJSON != "" {
		if err := json.Unmarshal([]byte(seasonalJSON), data.SeasonalProfiles); err != nil {
			return nil, fmt.Errorf("failed to unmarshal seasonal profiles: %w", err)
		}
	}

	// Deserialize pattern detector
	if patternJSON, ok := cm.Data[PatternDetectorKey]; ok && patternJSON != "" {
		if err := json.Unmarshal([]byte(patternJSON), data.PatternDetector); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pattern detector: %w", err)
		}
	}

	// Deserialize accuracy tracker
	if accuracyJSON, ok := cm.Data[AccuracyTrackerKey]; ok && accuracyJSON != "" {
		if err := json.Unmarshal([]byte(accuracyJSON), data.AccuracyTracker); err != nil {
			return nil, fmt.Errorf("failed to unmarshal accuracy tracker: %w", err)
		}
	}

	// Deserialize metadata
	if metadataJSON, ok := cm.Data[MetadataKey]; ok && metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &data.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}
	}

	return data, nil
}

// Delete removes the learning data ConfigMap
func (s *LearningStorage) Delete(ctx context.Context) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.configMapName,
			Namespace: s.namespace,
		},
	}

	err := s.client.Delete(ctx, cm)
	if errors.IsNotFound(err) {
		return nil
	}
	return err
}

// Exists checks if the ConfigMap exists
func (s *LearningStorage) Exists(ctx context.Context) (bool, error) {
	cm := &corev1.ConfigMap{}
	err := s.client.Get(ctx, types.NamespacedName{
		Name:      s.configMapName,
		Namespace: s.namespace,
	}, cm)

	if errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// GetConfigMapName returns the ConfigMap name
func (s *LearningStorage) GetConfigMapName() string {
	return s.configMapName
}

// GetStorageSize returns the approximate size of stored data in bytes
func (s *LearningStorage) GetStorageSize(ctx context.Context) (int, error) {
	cm := &corev1.ConfigMap{}
	err := s.client.Get(ctx, types.NamespacedName{
		Name:      s.configMapName,
		Namespace: s.namespace,
	}, cm)

	if errors.IsNotFound(err) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}

	totalSize := 0
	for _, v := range cm.Data {
		totalSize += len(v)
	}
	return totalSize, nil
}

// GetMetadata retrieves only the metadata from storage
func (s *LearningStorage) GetMetadata(ctx context.Context) (*StorageMetadata, error) {
	cm := &corev1.ConfigMap{}
	err := s.client.Get(ctx, types.NamespacedName{
		Name:      s.configMapName,
		Namespace: s.namespace,
	}, cm)

	if errors.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}

	metadataJSON, ok := cm.Data[MetadataKey]
	if !ok || metadataJSON == "" {
		return nil, nil
	}

	var metadata StorageMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

// CreateOwnerReference creates an owner reference for a BudAIScaler
func CreateOwnerReference(name, uid string, apiVersion, kind string) *metav1.OwnerReference {
	blockOwnerDeletion := true
	controller := true
	return &metav1.OwnerReference{
		APIVersion:         apiVersion,
		Kind:               kind,
		Name:               name,
		UID:                types.UID(uid),
		BlockOwnerDeletion: &blockOwnerDeletion,
		Controller:         &controller,
	}
}
