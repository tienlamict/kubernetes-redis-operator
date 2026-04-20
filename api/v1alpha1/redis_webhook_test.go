package v1alpha1

import (
	"testing"

	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func baseRedis() *Redis {
	return &Redis{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec: RedisSpec{
			Image: "redis:7.2-alpine",
		},
	}
}

// ---------- Default() ----------

func TestDefault_SetsImageDefaults(t *testing.T) {
	g := NewWithT(t)
	r := &Redis{ObjectMeta: metav1.ObjectMeta{Name: "x"}}
	r.Default()

	g.Expect(r.Spec.Image).To(Equal(DefaultRedisImage))
	g.Expect(r.Spec.ExporterImage).To(Equal(DefaultExporterImage))
	g.Expect(r.Spec.EnableExporter).NotTo(BeNil())
	g.Expect(*r.Spec.EnableExporter).To(BeTrue())
}

func TestDefault_DoesNotOverrideExistingValues(t *testing.T) {
	g := NewWithT(t)
	f := false
	r := &Redis{
		ObjectMeta: metav1.ObjectMeta{Name: "x"},
		Spec: RedisSpec{
			Image:          "custom:1.0",
			ExporterImage:  "custom-exporter:1.0",
			EnableExporter: &f,
		},
	}
	r.Default()

	g.Expect(r.Spec.Image).To(Equal("custom:1.0"))
	g.Expect(r.Spec.ExporterImage).To(Equal("custom-exporter:1.0"))
	g.Expect(*r.Spec.EnableExporter).To(BeFalse())
}

// ---------- ValidateCreate() ----------

func TestValidateCreate_NoStorage_OK(t *testing.T) {
	g := NewWithT(t)
	r := baseRedis()
	_, err := r.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidateCreate_StorageWithSize_OK(t *testing.T) {
	g := NewWithT(t)
	r := baseRedis()
	r.Spec.Storage = &StorageSpec{Size: "5Gi"}
	_, err := r.ValidateCreate()
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidateCreate_StorageEmptySize_Rejected(t *testing.T) {
	g := NewWithT(t)
	r := baseRedis()
	r.Spec.Storage = &StorageSpec{Size: ""}
	_, err := r.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("size"))
}

func TestValidateCreate_StorageInvalidQuantity_Rejected(t *testing.T) {
	g := NewWithT(t)
	r := baseRedis()
	r.Spec.Storage = &StorageSpec{Size: "not-a-quantity"}
	_, err := r.ValidateCreate()
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("valid resource.Quantity"))
}

// ---------- ValidateUpdate() ----------

func TestValidateUpdate_AddingStorageToExisting_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseRedis()
	newRedis := baseRedis()
	newRedis.Spec.Storage = &StorageSpec{Size: "5Gi"}

	_, err := newRedis.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot add storage"))
}

func TestValidateUpdate_RemovingStorageFromExisting_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseRedis()
	old.Spec.Storage = &StorageSpec{Size: "5Gi"}
	newRedis := baseRedis()

	_, err := newRedis.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot remove storage"))
}

func TestValidateUpdate_StorageShrink_Rejected(t *testing.T) {
	g := NewWithT(t)
	old := baseRedis()
	old.Spec.Storage = &StorageSpec{Size: "10Gi"}
	newRedis := baseRedis()
	newRedis.Spec.Storage = &StorageSpec{Size: "5Gi"}

	_, err := newRedis.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("cannot be decreased"))
}

func TestValidateUpdate_StorageGrow_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseRedis()
	old.Spec.Storage = &StorageSpec{Size: "5Gi"}
	newRedis := baseRedis()
	newRedis.Spec.Storage = &StorageSpec{Size: "10Gi"}

	_, err := newRedis.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidateUpdate_StorageClassChange_Rejected(t *testing.T) {
	g := NewWithT(t)
	oldClass := "standard"
	newClass := "premium"
	old := baseRedis()
	old.Spec.Storage = &StorageSpec{Size: "5Gi", ClassName: &oldClass}
	newRedis := baseRedis()
	newRedis.Spec.Storage = &StorageSpec{Size: "5Gi", ClassName: &newClass}

	_, err := newRedis.ValidateUpdate(old)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("immutable"))
}

func TestValidateUpdate_ImageChange_OK(t *testing.T) {
	g := NewWithT(t)
	old := baseRedis()
	newRedis := baseRedis()
	newRedis.Spec.Image = "redis:7.4-alpine"

	_, err := newRedis.ValidateUpdate(old)
	g.Expect(err).NotTo(HaveOccurred())
}

// ---------- ValidateDelete() ----------

func TestValidateDelete_AlwaysOK(t *testing.T) {
	g := NewWithT(t)
	r := baseRedis()
	_, err := r.ValidateDelete()
	g.Expect(err).NotTo(HaveOccurred())
}
