package kubernetes

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/disaster37/operator-sdk-extra/v2/pkg/helper"
	"github.com/disaster37/operator-sdk-extra/v2/pkg/test"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (t *ToolTestSuite) TestConsolidatedListAndDescribe() {
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
		doConsolidatedList(),
		doConsolidatedDescribe(),
	}
	testCase.PreTest = initConsolidatedTest
	testCase.Run()
}

func initConsolidatedTest(stepName *string, data map[string]any) (err error) {
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

func doConsolidatedList() test.TestStep[*corev1.ConfigMap] {
	return test.TestStep[*corev1.ConfigMap]{
		Name: "consolidatedList",
		Do: func(c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {
			return nil
		},
		Check: func(t *testing.T, c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {
			cfg := data["config"].(*rest.Config)
			ctx := context.Background()

			listTool, err := NewListTool(ctx, Configs{
				"test": &ClusterConfig{Config: cfg},
			})
			if err != nil {
				return err
			}

			_, err = listTool.Info(ctx)
			assert.NoError(t, err)

			// List all ConfigMaps in the namespace
			listCm, err := listTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "kind": "configmaps", "namespace": "%s"}`, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)

			// List with label selector
			listCm, err = listTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "kind": "configmaps", "namespace": "%s", "labelsSelector": "app=test"}`, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)

			// List with filter
			listCm, err = listTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "kind": "configmaps", "namespace": "%s", "filter": "-[2-3]"}`, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)

			// When cluster not exist, it should return error
			_, err = listTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "invalid-cluster", "kind": "configmaps", "namespace": "%s"}`, key.Namespace))
			assert.Error(t, err)

			// Without namespace, it should list ConfigMaps in all namespaces
			listCm, err = listTool.InvokableRun(ctx, `{"cluster": "test", "kind": "configmaps"}`)
			assert.NoError(t, err)
			assert.NotEmpty(t, listCm)

			// Unknown kind should return error
			_, err = listTool.InvokableRun(ctx, `{"cluster": "test", "kind": "UnknownKindXyz"}`)
			assert.Error(t, err)

			return nil
		},
	}
}

func doConsolidatedDescribe() test.TestStep[*corev1.ConfigMap] {
	return test.TestStep[*corev1.ConfigMap]{
		Name: "consolidatedDescribe",
		Do: func(c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {
			return nil
		},
		Check: func(t *testing.T, c client.Client, key types.NamespacedName, o *corev1.ConfigMap, data map[string]any) (err error) {
			cfg := data["config"].(*rest.Config)
			expectedCm := data["expectedCm"].(*corev1.ConfigMap)
			ctx := context.Background()

			describeTool, err := NewDescribeTool(ctx, Configs{
				"test": &ClusterConfig{Config: cfg},
			})
			if err != nil {
				return err
			}

			_, err = describeTool.Info(ctx)
			assert.NoError(t, err)

			// Describe the ConfigMap
			cm, err := describeTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "kind": "configmaps", "name": "%s", "namespace": "%s"}`, expectedCm.Name, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, cm)

			// Filter output by exclude fields
			cm, err = describeTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "kind": "configmaps", "name": "%s", "namespace": "%s", "excludeFieldsOutput": ["metadata", "status"]}`, expectedCm.Name, key.Namespace))
			assert.NoError(t, err)
			assert.NotEmpty(t, cm)

			// When cluster not exist, it should return error
			_, err = describeTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "invalid-cluster", "kind": "configmaps", "name": "%s", "namespace": "%s"}`, expectedCm.Name, key.Namespace))
			assert.Error(t, err)

			// When resource not exist, it should return error
			_, err = describeTool.InvokableRun(ctx, fmt.Sprintf(`{"cluster": "test", "kind": "configmaps", "name": "invalid-name", "namespace": "%s"}`, key.Namespace))
			assert.Error(t, err)

			return nil
		},
	}
}
