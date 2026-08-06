package s3

import (
	"github.com/cloudwego/eino/components/tool/utils"
)

func newListObjectsToolWithClients(configs Configs, clients map[string]Client) (*ListObjectsTool, error) {
	bt := newBaseToolWithClients(configs, clients)
	t := &ListObjectsTool{baseTool: bt}
	invokable, err := utils.InferTool("s3_list_objects", listObjectsDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable
	return t, nil
}

func newGetUsageToolWithClients(configs Configs, clients map[string]Client) (*GetUsageTool, error) {
	bt := newBaseToolWithClients(configs, clients)
	t := &GetUsageTool{baseTool: bt}
	invokable, err := utils.InferTool("s3_get_usage", getUsageDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable
	return t, nil
}

func newListObjectsWithSizeToolWithClients(configs Configs, clients map[string]Client) (*ListObjectsWithSizeTool, error) {
	bt := newBaseToolWithClients(configs, clients)
	t := &ListObjectsWithSizeTool{baseTool: bt}
	invokable, err := utils.InferTool("s3_list_objects_with_size", listObjectsWithSizeDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable
	return t, nil
}

func newGetLifecycleToolWithClients(configs Configs, clients map[string]Client) (*GetLifecycleTool, error) {
	bt := newBaseToolWithClients(configs, clients)
	t := &GetLifecycleTool{baseTool: bt}
	invokable, err := utils.InferTool("s3_get_lifecycle", getLifecycleDescription, t.Invoke)
	if err != nil {
		return nil, err
	}
	t.InvokableTool = invokable
	return t, nil
}
