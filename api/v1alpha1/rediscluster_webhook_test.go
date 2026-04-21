package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func baseCluster() *RedisCluster {
	return &RedisCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test-cluster", Namespace: "default"},
		Spec: RedisClusterSpec{
			Image:             "redis:7.2-alpine",
			Masters:           3,
			ReplicasPerMaster: 1,
		},
	}
}

// ---------- Default() ----------

func TestClusterDefault_SetsAllDefaults(t *testing.T) {
	g := NewWithT(t)
	rc := &RedisCluster{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	rc.Default()

	g.Expect(rc.Spec.Image).To(Equal(DefaultRedisImage))
	g.Expect(rc.Spec.ExporterImage).To(Equal(DefaultExporterImage))
	g.Expect(rc.Spec.EnableExporter).NotTo(BeNil())
	g.Expect(*rc.Spec.EnableExporter).To(BeTrue())
	g.Expect(rc.Spec.Masters).To(Equal(int32(3)))
	g.Expect(rc.Spec.RedisConfig).To(HaveKeyWithValue("cluster-node-timeout", "15000"))
	g.Expect(rc.Spec.RedisConfig).To(HaveKeyWithValue("appendonly", "yes"))
}

func TestClusterDefault_DoesNotOverrideExistingValues(t *testing.T) {
	g := NewWithT(t)
	f := false
	rc := &RedisCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec: RedisClusterSpec{
			Image:          "custom:1.0",
			ExporterImage:  "exporter:1.0",
			EnableExporter: &f,
			Masters:        5,
			RedisConfig:    map[string]string{"cluster-node-timeout": "30000"},
		},
	}
	rc.Default()

	g.Expect(rc.Spec.Image).To(Equal("custom:1.0"))
	g.Expect(rc.Spec.ExporterImage).To(Equal("exporter:1.0"))
	g.Expect(*rc.Spec.EnableExporter).To(BeFalse())
	g.Expect(rc.Spec.Masters).To(Equal(int32(5)))
	g.Expect(rc.Spec.RedisConfig).To(HaveKeyWithValue("cluster-node-timeout", "30000"))
}

func TestClusterDefault_SetsAppendOnlyIfMissing(t *testing.T) {
	g := NewWithT(t)
	rc := &RedisCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec:       RedisClusterSpec{RedisConfig: map[string]string{"cluster-node-timeout": "5000"}},
	}
	rc.Default()

	g.Expect(rc.Spec.RedisConfig).To(HaveKeyWithValue("appendonly", "yes"))
	g.Expect(rc.Spec.RedisConfig).To(HaveKeyWithValue("cluster-node-timeout", "5000"),
		"existing cluster-node-timeout must not be overwritten")
}

// ---------- ValidateCreate() ----------

func TestClusterValidateCreate_ValidSpec_OK(t *testing.T) {
	g := NewWithT(t)
	_, err := baseCluster().ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestClusterValidateCreate_MastersBelow3_Rejected(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.Masters = 2
	_, err := rc.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("masters"))
}

func TestClusterValidateCreate_NegativeReplicasPerMaster_Rejected(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.ReplicasPerMaster = -1
	_, err := rc.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("replicasPerMaster"))
}

func TestClusterValidateCreate_TotalNodesExceedsLimit_Rejected(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	// 10 masters × (1 + 10 replicas) = 110 nodes → exceeds 100.
	rc.Spec.Masters = 10
	rc.Spec.ReplicasPerMaster = 10
	_, err := rc.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("100"))
}

func TestClusterValidateCreate_EvenMasters_EmitsWarning(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.Masters = 4 // even
	warnings, err := rc.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(warnings).NotTo(BeEmpty())
	g.Expect(warnings[0]).To(ContainSubstring("odd"))
}

func TestClusterValidateCreate_OddMasters_NoWarning(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.Masters = 5 // odd
	warnings, err := rc.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(warnings).To(BeEmpty())
}

func TestClusterValidateCreate_StorageWithValidSize_OK(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.Storage = &StorageSpec{Size: "20Gi"}
	_, err := rc.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestClusterValidateCreate_StorageEmptySize_Rejected(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.Storage = &StorageSpec{Size: ""}
	_, err := rc.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("size"))
}

func TestClusterValidateCreate_StorageInvalidQuantity_Rejected(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Spec.Storage = &StorageSpec{Size: "notasize"}
	_, err := rc.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("valid resource.Quantity"))
}

// ---------- ValidateUpdate() ----------

func TestClusterValidateUpdate_ValidUpdate_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	newRC := baseCluster()
	newRC.Spec.Image = "redis:7.4-alpine"
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestClusterValidateUpdate_MastersBelow3_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	newRC := baseCluster()
	newRC.Spec.Masters = 2
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("masters"))
}

func TestClusterValidateUpdate_ScaleDownByMoreThanOne_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	old.Spec.Masters = 5
	newRC := baseCluster()
	newRC.Spec.Masters = 3 // scale down by 2 → rejected
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("more than 1"))
}

func TestClusterValidateUpdate_ScaleDownByOne_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	old.Spec.Masters = 5
	newRC := baseCluster()
	newRC.Spec.Masters = 4 // scale down by 1 → allowed
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestClusterValidateUpdate_ScalingPhase_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	old.Status.Phase = PhaseScaling
	newRC := baseCluster()
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring(PhaseScaling))
}

func TestClusterValidateUpdate_AddStorageToExisting_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	newRC := baseCluster()
	newRC.Spec.Storage = &StorageSpec{Size: "10Gi"}
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot add storage"))
}

func TestClusterValidateUpdate_RemoveStorageFromExisting_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	old.Spec.Storage = &StorageSpec{Size: "10Gi"}
	newRC := baseCluster()
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot remove storage"))
}

func TestClusterValidateUpdate_StorageShrink_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	old.Spec.Storage = &StorageSpec{Size: "20Gi"}
	newRC := baseCluster()
	newRC.Spec.Storage = &StorageSpec{Size: "10Gi"}
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot be decreased"))
}

func TestClusterValidateUpdate_StorageGrow_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseCluster()
	old.Spec.Storage = &StorageSpec{Size: "10Gi"}
	newRC := baseCluster()
	newRC.Spec.Storage = &StorageSpec{Size: "20Gi"}
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestClusterValidateUpdate_StorageClassChange_Rejected(t *testing.T) {
	g := NewWithT(t)
	oldClass := "standard"
	newClass := "premium"
	old := baseCluster()
	old.Spec.Storage = &StorageSpec{Size: "10Gi", ClassName: &oldClass}
	newRC := baseCluster()
	newRC.Spec.Storage = &StorageSpec{Size: "10Gi", ClassName: &newClass}
	_, err := newRC.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("immutable"))
}

// ---------- ValidateDelete() ----------

func TestClusterValidateDelete_ScalingPhase_Rejected(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Status.Phase = PhaseScaling
	_, err := rc.ValidateDelete()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring(PhaseScaling))
}

func TestClusterValidateDelete_NormalPhase_OK(t *testing.T) {
	g := NewWithT(t)
	rc := baseCluster()
	rc.Status.Phase = PhaseReady
	_, err := rc.ValidateDelete()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestClusterValidateDelete_EmptyPhase_OK(t *testing.T) {
	g := NewWithT(t)
	_, err := baseCluster().ValidateDelete()
	g.Expect(err).NotTo(HaveOccurred())
}
