package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/disaster37/operator-sdk-extra/v2/pkg/helper"
	"github.com/disaster37/operator-sdk-extra/v2/pkg/test"
	"github.com/google/go-cmp/cmp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (t *ToolTestSuite) TestConfigMap() {
	key := types.NamespacedName{
		Name:      "t-es-" + helper.RandomString(10),
		Namespace: "default",
	}
	data := map[string]any{
		"config": t.cfg,
		"client": t.k8sClient,
		"key":    key,
	}

	testCase := test.NewTestCase[*corev1.ConfigMap](t.T(), t.k8sClient, key, 5*time.Second, data)
	testCase.Steps = []test.TestStep[*corev1.ConfigMap]{
		doListConfigMap(),
		doDescribeConfigMap(),
	}

	testCase.PreTest = initConfigMapTest

	testCase.Run()
}

func initConfigMapTest(stepName *string, data map[string]any) (err error) {
	c := data["client"].(client.Client)
	key := data["key"].(types.NamespacedName)

	ctx := context.Background()

	cm1 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-1", key.Name),
			Namespace: key.Namespace,
		},
		Data: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}
	if err = c.Create(ctx, cm1); err != nil {
		return err
	}

	cm2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-2", key.Name),
			Namespace: key.Namespace,
		},
		Data: map[string]string{
			"key3": "value3",
			"key4": "value4",
		},
	}
	if err = c.Create(ctx, cm2); err != nil {
		return err
	}

	cm3 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-3", key.Name),
			Namespace: key.Namespace,
			Labels: map[string]string{
				"app": "test",
			},
		},
		Data: map[string]string{
			"key5": "value5",
			"key6": "value6",
		},
	}
	if err = c.Create(ctx, cm3); err != nil {
		return err
	}

	data["expectedCm"] = cm1

	logrus.Infof("Init test for ConfigMap %s/%s successfully\n\n", key.Namespace, key.Name)

	return nil
}

func doListConfigMap() test.TestStep[*corev1.ConfigMap] {
	return test.TestStep[*corev1.ConfigMap]{
		Name: "listConfigMap",
		Do: func(c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {
			return nil
		},
		Check: func(t *testing.T, c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {

			cfg := data["config"].(*rest.Config)
			ctx := context.Background()

			cmToolList, err := NewConfigMapListTool(ctx, Configs{
				"test": cfg,
			})
			if err != nil {
				return err
			}

			_, err = cmToolList.Info(ctx)
			assert.NoError(t, err)

			// List all ConfigMaps in the namespace
			listCm, err := cmToolList.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "namespace": "%s"}`, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)
			expectedOutputs := []ConfigMapListOutput{
				{
					Name:      fmt.Sprintf("%s-1", key.Name),
					Namespace: key.Namespace,
				},
				{
					Name:      fmt.Sprintf("%s-2", key.Name),
					Namespace: key.Namespace,
				},
				{
					Name:      fmt.Sprintf("%s-3", key.Name),
					Namespace: key.Namespace,
				},
			}
			assert.Empty(t, cmp.Diff(listCm, string(MustMarshal(expectedOutputs))))

			// List ConfigMaps with label selector
			listCm, err = cmToolList.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "namespace": "%s", "labelsSelector": "app=test"}`, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)
			expectedOutputs = []ConfigMapListOutput{
				{
					Name:      fmt.Sprintf("%s-3", key.Name),
					Namespace: key.Namespace,
				},
			}
			assert.Empty(t, cmp.Diff(listCm, string(MustMarshal(expectedOutputs))))

			// List ConfigMaps with filter
			listCm, err = cmToolList.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "namespace": "%s", "filter": "-[2-3]"}`, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)
			expectedOutputs = []ConfigMapListOutput{
				{
					Name:      fmt.Sprintf("%s-2", key.Name),
					Namespace: key.Namespace,
				},
				{
					Name:      fmt.Sprintf("%s-3", key.Name),
					Namespace: key.Namespace,
				},
			}
			assert.Empty(t, cmp.Diff(listCm, string(MustMarshal(expectedOutputs))))

			// When cluster not exist, it should return error
			_, err = cmToolList.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "invalid-cluster", "namespace": "%s"}`, key.Namespace))
			assert.Error(t, err)

			// Without namespace, it should list ConfigMaps in all namespaces
			listCm, err = cmToolList.InvokableRun(ctx, `{"cluster": "test"}`)
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)

			return nil
		},
	}
}

func doDescribeConfigMap() test.TestStep[*corev1.ConfigMap] {
	return test.TestStep[*corev1.ConfigMap]{
		Name: "describeConfigMap",
		Do: func(c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {
			return nil
		},
		Check: func(t *testing.T, c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {

			cfg := data["config"].(*rest.Config)
			expectedCm := data["expectedCm"].(*corev1.ConfigMap)
			ctx := context.Background()

			if c.Get(ctx, types.NamespacedName{Namespace: key.Namespace, Name: expectedCm.Name}, expectedCm) != nil {
				return fmt.Errorf("failed to get expected ConfigMap %s/%s", key.Namespace, expectedCm.Name)
			}

			cmToolDescribe, err := NewConfigMapDescribeTool(ctx, Configs{
				"test": cfg,
			})
			if err != nil {
				return err
			}

			_, err = cmToolDescribe.Info(ctx)
			assert.NoError(t, err)

			// Describe the ConfigMap
			cm, err := cmToolDescribe.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "name": "%s", "namespace": "%s"}`, expectedCm.Name, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, cm)
			assert.Empty(t, cmp.Diff(cm, string(MustMarshal(objectToDescribeOutput(expectedCm)))))

			// Filter output by exclude fields
			cm, err = cmToolDescribe.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "name": "%s", "namespace": "%s", "excludeFieldsOutput": ["metadata", "status"]}`, expectedCm.Name, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, cm)
			describeOutput := objectToDescribeOutput(expectedCm)
			describeOutput.Metadata = nil
			describeOutput.Status = nil
			assert.Empty(t, cmp.Diff(cm, string(MustMarshal(describeOutput))))

			// When cluster not exist, it should return error
			_, err = cmToolDescribe.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "invalid-cluster", "name": "%s", "namespace": "%s"}`, expectedCm.Name, key.Namespace))
			assert.Error(t, err)

			// When ConfigMap not exist, it should return error
			_, err = cmToolDescribe.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "name": "invalid-name", "namespace": "%s"}`, key.Namespace))
			assert.Error(t, err)

			// When namespace not provided, it should return error
			_, err = cmToolDescribe.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "name": "%s"}`, expectedCm.Name))
			assert.Error(t, err)

			return nil
		},
	}
}
