package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func baseSentinel() *RedisSentinel {
	return &RedisSentinel{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ha", Namespace: "default"},
		Spec: RedisSentinelSpec{
			Image:            "redis:7.2-alpine",
			Replicas:         3,
			SentinelReplicas: 3,
		},
	}
}

// ---------- Default() ----------

func TestSentinelDefault_SetsAllDefaults(t *testing.T) {
	g := NewWithT(t)
	rs := &RedisSentinel{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	rs.Default()

	g.Expect(rs.Spec.Image).To(Equal(DefaultRedisImage))
	g.Expect(rs.Spec.ExporterImage).To(Equal(DefaultExporterImage))
	g.Expect(rs.Spec.EnableExporter).NotTo(BeNil())
	g.Expect(*rs.Spec.EnableExporter).To(BeTrue())
	g.Expect(rs.Spec.Replicas).To(Equal(int32(3)))
	g.Expect(rs.Spec.SentinelReplicas).To(Equal(int32(3)))
	g.Expect(rs.Spec.SentinelImage).To(Equal(DefaultRedisImage)) // mirrors Image
}

func TestSentinelDefault_DoesNotOverrideExistingValues(t *testing.T) {
	g := NewWithT(t)
	f := false
	rs := &RedisSentinel{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec: RedisSentinelSpec{
			Image:            "custom:1.0",
			SentinelImage:    "sentinel:1.0",
			SentinelReplicas: 5,
			Replicas:         5,
			EnableExporter:   &f,
		},
	}
	rs.Default()

	g.Expect(rs.Spec.Image).To(Equal("custom:1.0"))
	g.Expect(rs.Spec.SentinelImage).To(Equal("sentinel:1.0"))
	g.Expect(rs.Spec.Replicas).To(Equal(int32(5)))
	g.Expect(rs.Spec.SentinelReplicas).To(Equal(int32(5)))
	g.Expect(*rs.Spec.EnableExporter).To(BeFalse())
}

// ---------- ValidateCreate() ----------

func TestSentinelValidateCreate_ValidSpec_OK(t *testing.T) {
	g := NewWithT(t)
	_, err := baseSentinel().ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestSentinelValidateCreate_ReplicasBelowThree_Rejected(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.Replicas = 2
	_, err := rs.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("replicas"))
}

func TestSentinelValidateCreate_SentinelReplicasBelowThree_Rejected(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.SentinelReplicas = 2
	_, err := rs.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("sentinelReplicas"))
}

func TestSentinelValidateCreate_EvenSentinelReplicas_Rejected(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.SentinelReplicas = 4
	_, err := rs.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("odd"))
}

func TestSentinelValidateCreate_ValidOddSentinel5_OK(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.SentinelReplicas = 5
	_, err := rs.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestSentinelValidateCreate_StorageWithValidSize_OK(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.Storage = &StorageSpec{Size: "10Gi"}
	_, err := rs.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestSentinelValidateCreate_StorageEmptySize_Rejected(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.Storage = &StorageSpec{Size: ""}
	_, err := rs.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("size"))
}

func TestSentinelValidateCreate_StorageInvalidQuantity_Rejected(t *testing.T) {
	g := NewWithT(t)
	rs := baseSentinel()
	rs.Spec.Storage = &StorageSpec{Size: "invalid"}
	_, err := rs.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("valid resource.Quantity"))
}

// ---------- ValidateUpdate() ----------

func TestSentinelValidateUpdate_ValidUpdate_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	newRS := baseSentinel()
	newRS.Spec.Image = "redis:7.4-alpine"
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestSentinelValidateUpdate_ScaleReplicasBelow3_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	newRS := baseSentinel()
	newRS.Spec.Replicas = 1
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
}

func TestSentinelValidateUpdate_ScaleSentinelBelow3_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	newRS := baseSentinel()
	newRS.Spec.SentinelReplicas = 2
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
}

func TestSentinelValidateUpdate_AddStorageToExisting_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	newRS := baseSentinel()
	newRS.Spec.Storage = &StorageSpec{Size: "10Gi"}
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot add storage"))
}

func TestSentinelValidateUpdate_RemoveStorageFromExisting_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	old.Spec.Storage = &StorageSpec{Size: "10Gi"}
	newRS := baseSentinel()
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot remove storage"))
}

func TestSentinelValidateUpdate_StorageShrink_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	old.Spec.Storage = &StorageSpec{Size: "20Gi"}
	newRS := baseSentinel()
	newRS.Spec.Storage = &StorageSpec{Size: "10Gi"}
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot be decreased"))
}

func TestSentinelValidateUpdate_StorageGrow_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseSentinel()
	old.Spec.Storage = &StorageSpec{Size: "10Gi"}
	newRS := baseSentinel()
	newRS.Spec.Storage = &StorageSpec{Size: "20Gi"}
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestSentinelValidateUpdate_StorageClassChange_Rejected(t *testing.T) {
	g := NewWithT(t)
	oldClass := "standard"
	newClass := "premium"
	old := baseSentinel()
	old.Spec.Storage = &StorageSpec{Size: "10Gi", ClassName: &oldClass}
	newRS := baseSentinel()
	newRS.Spec.Storage = &StorageSpec{Size: "10Gi", ClassName: &newClass}
	_, err := newRS.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("immutable"))
}

// ---------- ValidateDelete() ----------

func TestSentinelValidateDelete_AlwaysOK(t *testing.T) {
	g := NewWithT(t)
	_, err := baseSentinel().ValidateDelete()
	g.Expect(err).NotTo(HaveOccurred())
}
