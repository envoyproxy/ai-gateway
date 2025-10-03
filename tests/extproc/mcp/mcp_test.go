// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"github.com/stretchr/testify/require"
	v1 "go.opentelemetry.io/proto/otlp/common/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
)

const (
	defaultMCPBackendResourcePrefix = "default-mcp-backend__"
	defaultMCPPath                  = "/mcp"
)

func TestMCP(t *testing.T) {
	tests := []struct {
		name   string
		testFn func(t *testing.T, m *mcpEnv)
	}{
		{name: "ListTools", testFn: testListTools},
		{name: "ToolCall", testFn: testToolCall},
		{name: "ToolCallDumbEcho", testFn: testToolCallDumbEcho},
		{name: "ToolCallError", testFn: testToolCallError},
		{name: "ToolCountDown", testFn: testToolCountDown},
		{name: "Ping", testFn: testPing},
		{name: "LoggingSetLevel", testFn: testLoggingSetLevel},
		{name: "ListPrompts", testFn: testListPrompts},
		{name: "CodeReviewPrompts", testFn: testCodeReviewPrompts},
		{name: "PromptChangeNotifications", testFn: testPromptChangeNotifications},
		{name: "ListResources", testFn: testListResources},
		{name: "ReadResource", testFn: testReadResource},
		{name: "ReadResourceNotFound", testFn: testReadResourceNotFound},
		{name: "ListResourceTemplates", testFn: testListResourceTemplates},
		{name: "ResourceSubscribe", testFn: testResourceSubscribe},
		{name: "ResourceListChangeNotifications", testFn: testResourceListChangeNotifications},
		{name: "ListRootsAndChangeRoots", testFn: testListRootsAndChangeRoots},
		{name: "SamplingCreateMessage", testFn: testSamplingCreateMessage},
		{name: "Elicit", testFn: testElicit},
		{name: "NotificationCancelled", testFn: testNotificationCancelled},
		{name: "Complete", testFn: testComplete},
	}
	_ = tests

	t.Run("default/force_json=true", func(t *testing.T) {
		env := requireNewMCPEnv(t, true, 1200*time.Second, defaultMCPPath)
		for _, tc := range tests {
			t.Run(tc.name+"/force_json=true", func(t *testing.T) {
				tc.testFn(t, env)
			})
		}
	})
	t.Run("default/force_json=false", func(t *testing.T) {
		env := requireNewMCPEnv(t, false, 1200*time.Second, defaultMCPPath)
		for _, tc := range tests {
			t.Run(tc.name+"/force_json=false", func(t *testing.T) {
				tc.testFn(t, env)
			})
		}
	})
	t.Run("custom_write_timeout", func(t *testing.T) {
		env := requireNewMCPEnv(t, false, 2*time.Second, defaultMCPPath)
		for _, tc := range tests {
			t.Run(tc.name+"/custom_write_timeout", func(t *testing.T) {
				tc.testFn(t, env)
			})
		}
	})
	t.Run("/mcp/yet/another/path", func(t *testing.T) {
		env := requireNewMCPEnv(t, false, 1200*time.Second, "/mcp/yet/another/path")
		t.Run("call", func(t *testing.T) {
			testToolCallDumbEcho(t, env)
		})
		t.Run("list", func(t *testing.T) {
			testListToolsRequireOnlyDumb(t, env)
		})
	})
	t.Run("/awesome-path", func(t *testing.T) {
		env := requireNewMCPEnv(t, false, 1200*time.Second, "/awesome-path")
		t.Run("call", func(t *testing.T) {
			testToolCallDumbEcho(t, env)
		})
		t.Run("list", func(t *testing.T) {
			testListToolsRequireOnlyDumb(t, env)
		})
	})
}

func testListTools(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	tools, err := s.session.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	span := m.collector.TakeSpan()
	requireKeyValue(t, span.Attributes, "mcp.method.name", stringAnyValue("tools/list"))
	// equal to the count of backends.
	require.Len(t, span.Events, 2)

	// Hardcode names rather than using testmcp.*Tool.Tool.Name because some
	// tools are created dynamically (e.g. add_prompt).
	require.ElementsMatch(t, names, []string{
		"dumb-mcp-backend__" + testmcp.ToolDumbEcho.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolEcho.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolSum.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolError.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolCountDown.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolContainsRootTool.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolDelay.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolAddPromptName,
		"default-mcp-backend__" + testmcp.ToolResourceUpdateNotificationName,
		"default-mcp-backend__" + testmcp.ToolAddOrDeleteDummyResourceName,
		"default-mcp-backend__" + testmcp.ToolNotificationCountsName,
		"default-mcp-backend__" + testmcp.ToolElicitEmail.Tool.Name,
		"default-mcp-backend__" + testmcp.ToolCreateMessage.Tool.Name,
	})
}

func testListToolsRequireOnlyDumb(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	tools, err := s.session.ListTools(t.Context(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	require.ElementsMatch(t, names, []string{
		"dumb-mcp-backend__" + testmcp.ToolDumbEcho.Tool.Name,
	})
}

func testToolCall(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)

	const helloText = "hello MCP over HTTP 👋"
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolEcho.Tool.Name,
		Arguments: testmcp.ToolEchoArgs{Text: helloText},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Equal(t, helloText, res.Content[0].(*mcp.TextContent).Text)
	span := m.collector.TakeSpan()
	requireKeyValue(t, span.Attributes, "mcp.method.name", stringAnyValue("tools/call"))
	require.Len(t, span.Events, 1)
	require.Equal(t, "route to backend", span.Events[0].Name)

	res, err = s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolSum.Tool.Name,
		Arguments: testmcp.ToolSumArgs{A: 41, B: 1},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Equal(t, "42", res.Content[0].(*mcp.TextContent).Text)
	span = m.collector.TakeSpan()
	requireKeyValue(t, span.Attributes, "mcp.method.name", stringAnyValue("tools/call"))
}

func testToolCallDumbEcho(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)

	const helloText = "hello MCP over HTTP 👋"
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "dumb-mcp-backend__" + testmcp.ToolDumbEcho.Tool.Name,
		Arguments: testmcp.ToolEchoArgs{Text: helloText},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Equal(t, "dumb echo: "+helloText, res.Content[0].(*mcp.TextContent).Text)
}

func testToolCallError(t *testing.T, m *mcpEnv) {
	// Tool execution errors are returned in the content so that the LLM
	// can process the messages and react to them.
	s := m.newSession(t)
	t.Run("tool error", func(t *testing.T) {
		const errTool = "tool error"
		res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "default-mcp-backend__" + testmcp.ToolError.Tool.Name,
			Arguments: testmcp.ToolErrorArgs{Error: errTool},
		})
		require.NoError(t, err)
		require.True(t, res.IsError)
		require.Len(t, res.Content, 1)
		require.IsType(t, &mcp.TextContent{}, res.Content[0])
		require.Equal(t, errTool, res.Content[0].(*mcp.TextContent).Text)
	})

	// Protocol errors or tool invocation errors (such as validation errors) are
	// returned as errors.
	t.Run("validation error", func(t *testing.T) {
		res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "default-mcp-backend__" + testmcp.ToolError.Tool.Name,
			Arguments: testmcp.ToolErrorArgs{},
		})
		require.Error(t, err)
		require.Nil(t, res)
		require.Contains(t, err.Error(), "minLength")
	})
}

// TestToolCountDown tests a tool that sends progress notifications.
//
// Inside the tool handler, it will send progress notifications every interval
// until it reaches zero. The test verifies that the notifications are received
// in the correct order without blocking the entire stream.
func testToolCountDown(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	const count = 5
	const interval = time.Millisecond * 500

	// MCP server will disconnect after writeTimeout, then reconnection happen.
	// Client reconnect with Last-Event-ID, so that it will not receive duplicated
	// notifications.
	waitTimeOut := 5 * m.writeTimeout
	err := s.session.SetLoggingLevel(t.Context(), &mcp.SetLoggingLevelParams{
		Level: "error",
	})
	require.NoError(t, err)

	var res *mcp.CallToolResult
	callErrorCh := make(chan error, 1)
	var doneBool atomic.Bool
	go func() {
		res, err = s.session.CallTool(t.Context(), &mcp.CallToolParams{
			Name:      "default-mcp-backend__" + testmcp.ToolCountDown.Tool.Name,
			Arguments: testmcp.ToolCountDownArgs{From: count, Interval: interval.String()},
		})
		callErrorCh <- err
		doneBool.Store(true)
	}()

	// we cannot assume the order of notifications, so we use a set to track them.
	counts := sets.New[int]()
	for i := range count + 1 {
		counts.Insert(i)
	}

	expectedLogs := sets.New[string]()
	// we cannot assume the order of progress, so we just check if we received one.
	for range count + 1 {
		var notif *mcp.ProgressNotificationClientRequest
		select {
		case notif = <-s.progressNotifications:
		case <-time.After(waitTimeOut):
			t.Fatal("timeout waiting for progress notification")
		}
		n := int(notif.Params.ProgressToken.(float64))
		t.Log("Receive progress", slog.Int("n", n))
		expectedMsg := "count down: " + fmt.Sprint(count-n)
		expectedLogs.Insert(expectedMsg)
		require.NotNil(t, notif)
		require.NotNil(t, notif.Params)
		require.Equal(t, expectedMsg, notif.Params.Message)
		require.Contains(t, counts.UnsortedList(), n)
		counts.Delete(n)
	}

	// we cannot assume the order of logging messages, so we just check if we received one.
	for range count + 1 {
		var loggingNotif *mcp.LoggingMessageRequest
		select {
		case loggingNotif = <-s.loggingNotification:
		case <-time.After(waitTimeOut):
			t.Fatal("timeout waiting for logging notification")
		}
		require.NotNil(t, loggingNotif)
		require.NotNil(t, loggingNotif.Params)
		param := loggingNotif.Params
		t.Log("Receive log", slog.Any("data", param.Data))
		require.Equal(t, mcp.LoggingLevel("error"), param.Level)
		require.Contains(t, param.Data, "count down: ")
		expectedLogs.Delete(param.Data.(string))
	}

	require.Empty(t, expectedLogs)
	require.Empty(t, counts)

	err = <-callErrorCh
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return doneBool.Load() == true
	}, time.Second, time.Millisecond*10)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Equal(t, "Done!", res.Content[0].(*mcp.TextContent).Text)
}

func testPing(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)

	err := s.session.Ping(t.Context(), &mcp.PingParams{})
	require.NoError(t, err)
}

func testLoggingSetLevel(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	err := s.session.SetLoggingLevel(t.Context(), &mcp.SetLoggingLevelParams{
		Level: "debug",
	})
	require.NoError(t, err)
}

func testListPrompts(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	list, err := s.session.ListPrompts(t.Context(), &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, list.Prompts, 1)
	require.Equal(t, defaultMCPBackendResourcePrefix+testmcp.CodeReviewPrompt.Name, list.Prompts[0].Name)
	require.Equal(t, testmcp.CodeReviewPrompt.Description, list.Prompts[0].Description)
}

func testCodeReviewPrompts(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)

	resp, err := s.session.GetPrompt(t.Context(),
		&mcp.GetPromptParams{Name: defaultMCPBackendResourcePrefix + "code_review", Arguments: map[string]string{"Code": "1+1"}})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "Code review prompt", resp.Description)
	require.Len(t, resp.Messages, 1)
	require.Equal(t, mcp.Role("user"), resp.Messages[0].Role)
	require.IsType(t, &mcp.TextContent{}, resp.Messages[0].Content)
	require.Contains(t, resp.Messages[0].Content.(*mcp.TextContent).Text, "Please review the following code: 1+1")
}

func testPromptChangeNotifications(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	list, err := s.session.ListPrompts(t.Context(), &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, list.Prompts, 1)

	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "default-mcp-backend__" + testmcp.ToolAddPromptName,
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Wait for the notification.
	var req *mcp.PromptListChangedRequest
	select {
	case req = <-s.promptListChangedNotifications:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for prompt change notification")
	}
	require.NotNil(t, req)
	require.NotNil(t, req.Params)
	require.IsTypef(t, &mcp.PromptListChangedParams{}, req.Params, "expected PromptListChangedParams, got %T", req.Params)

	// Verify the prompt was updated.
	list, err = s.session.ListPrompts(t.Context(), &mcp.ListPromptsParams{})
	require.NoError(t, err)
	require.Len(t, list.Prompts, 2)
}

func testListResources(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	list, err := s.session.ListResources(t.Context(), &mcp.ListResourcesParams{})
	require.NoError(t, err)
	require.Len(t, list.Resources, 1)
	require.Equal(t, defaultMCPBackendResourcePrefix+testmcp.DummyResource.Name, list.Resources[0].Name)
	require.Equal(t, testmcp.DummyResource.Description, list.Resources[0].Description)
}

func testReadResource(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	r, err := s.session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: defaultMCPBackendResourcePrefix + "file:///dummy.txt",
	})
	require.NoError(t, err)
	require.Len(t, r.Contents, 1)
	require.Equal(t, testmcp.DummyResource.URI, r.Contents[0].URI)
	require.Equal(t, testmcp.DummyResource.MIMEType, r.Contents[0].MIMEType)
	require.Equal(t, "dummy", string(r.Contents[0].Blob))
}

func testReadResourceNotFound(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	r, err := s.session.ReadResource(t.Context(), &mcp.ReadResourceParams{
		URI: defaultMCPBackendResourcePrefix + "file:///notfound.txt",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "Resource not found")
	require.Nil(t, r)
}

func testListResourceTemplates(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	list, err := s.session.ListResourceTemplates(t.Context(), &mcp.ListResourceTemplatesParams{})
	require.NoError(t, err)
	require.Len(t, list.ResourceTemplates, 1)
	require.Equal(t, defaultMCPBackendResourcePrefix+testmcp.DummyResourceTemplate.Name, list.ResourceTemplates[0].Name)
	require.Equal(t, testmcp.DummyResourceTemplate.Description, list.ResourceTemplates[0].Description)
}

func testResourceSubscribe(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	list, err := s.session.ListResources(t.Context(), &mcp.ListResourcesParams{})
	require.NoError(t, err)
	require.Len(t, list.Resources, 1)
	require.Equal(t, defaultMCPBackendResourcePrefix+testmcp.DummyResource.Name, list.Resources[0].Name)
	require.Equal(t, testmcp.DummyResource.Description, list.Resources[0].Description)

	err = s.session.Subscribe(t.Context(), &mcp.SubscribeParams{
		URI: defaultMCPBackendResourcePrefix + list.Resources[0].URI,
	})
	require.NoError(t, err)

	// Update the resource.
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolResourceUpdateNotificationName,
		Arguments: map[string]any{"uri": list.Resources[0].URI},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	// Wait for the subscribe notification.
	requireEventuallyNotificationCountMessages(t, s, "subscribe: 1")

	// Wait for the notification.
	var req *mcp.ResourceUpdatedNotificationRequest
	select {
	case req = <-s.resourceUpdatedNotifications:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for resource updated notification")
	}
	require.NotNil(t, req)
	require.NotNil(t, req.Params)
	require.IsTypef(t, &mcp.ResourceUpdatedNotificationParams{}, req.Params, "expected ResourceUpdatedNotificationRequest, got %T", req.Params)

	err = s.session.Unsubscribe(t.Context(), &mcp.UnsubscribeParams{
		URI: defaultMCPBackendResourcePrefix + list.Resources[0].URI,
	})
	require.NoError(t, err)
	// Wait for the unsubscribe notification.
	requireEventuallyNotificationCountMessages(t, s, "unsubscribe: 1")

	res, err = s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolResourceUpdateNotificationName,
		Arguments: map[string]any{"uri": list.Resources[0].URI},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Wait for the notification.
	select {
	case req = <-s.resourceUpdatedNotifications:
		t.Fatal("received unexpected resource updated notification after unsubscribe: ", req)
	case <-time.After(2 * time.Second):

	}
}

func testResourceListChangeNotifications(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	list, err := s.session.ListResources(t.Context(), &mcp.ListResourcesParams{})
	require.NoError(t, err)
	require.Len(t, list.Resources, 1)

	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolAddOrDeleteDummyResourceName,
		Arguments: map[string]any{"delete": false},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	// Clean up, otherwise it will affect ListResources in other tests.
	t.Cleanup(func() {
		res, err = s.session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "default-mcp-backend__" + testmcp.ToolAddOrDeleteDummyResourceName,
			Arguments: map[string]any{"delete": true},
		})
		require.NoError(t, err)
		require.False(t, res.IsError)
	})

	// Wait for the notification.
	var req *mcp.ResourceListChangedRequest
	select {
	case req = <-s.resourceListChangedNotifications:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for resource list change notification")
	}
	require.NotNil(t, req)
	require.NotNil(t, req.Params)
	require.IsTypef(t, &mcp.ResourceListChangedParams{}, req.Params, "expected ResourceListChangedParams, got %T", req.Params)

	// Verify the resource was updated.
	list, err = s.session.ListResources(t.Context(), &mcp.ListResourcesParams{})
	require.NoError(t, err)
	require.Len(t, list.Resources, 2)
}

func testListRootsAndChangeRoots(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolContainsRootTool.Tool.Name,
		Arguments: testmcp.ToolContainsRootToolArgs{ExpectedRootName: mcpDefaultRootName},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, fmt.Sprintf("root %q found", mcpDefaultRootName))

	m.mux.Lock()
	defer m.mux.Unlock()
	// This will trigger a notifications/roots/list_changed notification from client to server.
	m.client.RemoveRoots(mcpDefaultRootURI)
	requireEventuallyNotificationCountMessages(t, s, "roots_list_changed: 1")

	// Now the default root should not be found.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	res, err = s.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "default-mcp-backend__" + testmcp.ToolContainsRootTool.Tool.Name,
		Arguments: testmcp.ToolContainsRootToolArgs{ExpectedRootName: mcpDefaultRootName},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Len(t, res.Content, 1)
	require.IsType(t, &mcp.TextContent{}, res.Content[0])
	require.Contains(t, res.Content[0].(*mcp.TextContent).Text, fmt.Sprintf("root %q not found", mcpDefaultRootName))

	requireMetricGreaterThan(t, m, "mcp_method_count_total", map[string]string{
		"mcp_method_name": "notifications/resources/list_changed",
	}, 0)

	requireMetricGreaterThan(t, m, "mcp_method_count_total", map[string]string{
		"mcp_method_name": "notifications/initialized",
	}, 0)
	requireMetricGreaterThan(t, m, "mcp_capabilities_negotiated_total", map[string]string{
		"capability_type": "resources",
		"capability_side": "server",
	}, 0)
}

func testSamplingCreateMessage(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "default-mcp-backend__" + testmcp.ToolCreateMessage.Tool.Name,
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Wait for the request from the server.
	var req *mcp.CreateMessageRequest
	select {
	case req = <-s.createMessageRequests:
		progressToken := req.Params.Meta["progressToken"]
		err = s.session.NotifyProgress(t.Context(), &mcp.ProgressNotificationParams{
			Message:       "foo",
			Progress:      12345,
			ProgressToken: progressToken,
		})
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for create message request")
	}
	require.NotNil(t, req)
	require.NotNil(t, req.Params)
	require.IsTypef(t, &mcp.CreateMessageParams{}, req.Params, "expected CreateMessageParams, got %T", req.Params)

	requireMetricGreaterThan(t, m, "mcp_progress_notifications_total", nil, 0)
}

func testElicit(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "default-mcp-backend__" + testmcp.ToolElicitEmail.Tool.Name,
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	// Wait for the request from the server.
	var req *mcp.ElicitRequest
	select {
	case req = <-s.elicitRequests:
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for elicit request")
	}
	require.NotNil(t, req)
	require.NotNil(t, req.Params)
	require.IsTypef(t, &mcp.ElicitParams{}, req.Params, "expected ElicitParams, got %T", req.Params)
}

func testNotificationCancelled(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)

	ctx, cancel := context.WithCancel(t.Context()) //nolint: govet
	doneCh := make(chan any)
	go func() {
		_, _ = s.session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "default-mcp-backend__" + testmcp.ToolDelay.Tool.Name,
			Arguments: testmcp.ToolDelayArgs{Duration: "1s"},
		})
		doneCh <- struct{}{}
	}()

	select {
	case <-time.After(time.Microsecond * 500):
		cancel()
		// we cannot do the test in TearDownSuite,
		// we need to wait a while for notifications/cancelled,
		// metric won't be updated if the test exits too early.
		requireMetricGreaterThan(t, m, "mcp_method_count_total", map[string]string{
			"mcp_method_name": "notifications/cancelled",
		}, 0)
	case <-doneCh:
		t.Fatal("CallTool returned before the delay")
	}
}

func testComplete(t *testing.T, m *mcpEnv) {
	s := m.newSession(t)
	result, err := s.session.Complete(t.Context(), &mcp.CompleteParams{
		Argument: mcp.CompleteParamsArgument{
			Name:  "language",
			Value: "py",
		},
		Ref: &mcp.CompleteReference{
			Type: "ref/prompt",
			Name: defaultMCPBackendResourcePrefix + "code_review",
		},
	})
	require.NoError(t, err)
	completionValues := []string{"python", "pytorch", "pyside"}
	require.Equal(t, completionValues, result.Completion.Values)
}

var metricParser = expfmt.NewTextParser(model.UTF8Validation)

// getCounterMetricByNameLabels retrieves a counter metric by name and labels from the given Prometheus metrics URL.
func getCounterMetricByNameLabels(url string, timeout time.Duration, name string, labels map[string]string) (float64, error) {
	metrics, err := retrieveMetrics(url, timeout)
	if err != nil {
		return 0, err
	}
	metricFamily, ok := metrics[name]
	if !ok {
		return 0, fmt.Errorf("metric %q not found", name)
	}
	var result float64
	var matched bool
	for _, m := range metricFamily.Metric {
		if len(m.Label) < len(labels) {
			continue
		}
		match := true
		for k, v := range labels {
			found := false
			for _, label := range m.Label {
				if label.GetName() == k && label.GetValue() == v {
					found = true
					break
				}
			}
			if !found {
				match = false
				break
			}
		}
		if match {
			if metricFamily.GetType() != dto.MetricType_COUNTER {
				return 0, fmt.Errorf("metric %q is not a counter", name)
			}
			result += m.GetCounter().GetValue()
			matched = true
		}
	}
	if matched {
		return result, nil
	}
	return 0, fmt.Errorf("metric %q with labels %v not found", name, labels)
}

func retrieveMetrics(url string, timeout time.Duration) (map[string]*dto.MetricFamily, error) {
	httpClient := http.Client{
		Timeout: timeout,
	}
	res, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("failed to scrape metrics: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to scrape metrics: %s", res.Status)
	}

	return metricParser.TextToMetricFamilies(res.Body)
}

const (
	retrieveMetricsTime = time.Second * 5
	retrieveMetricsTick = time.Millisecond * 100
)

func requireMetricGreaterThan(t *testing.T, m *mcpEnv, metricName string, metricLabels map[string]string, prev float64) {
	require.Eventually(t, func() bool {
		current, err := getCounterMetricByNameLabels(m.extProcMetricsURL, retrieveMetricsTime, metricName, metricLabels)
		if err != nil {
			t.Log("failed to get metric: ", err)
			return false
		}
		return current > prev
	}, retrieveMetricsTime, retrieveMetricsTick)
}

func requireEventuallyNotificationCountMessages(t *testing.T, s *mcpSession, expected string) {
	require.Eventually(t, func() bool {
		res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "default-mcp-backend__" + testmcp.ToolNotificationCountsName,
		})
		if err != nil {
			t.Log("error calling tool: ", err)
			return false
		}
		if res.IsError {
			t.Log("tool returned error: ", res.Content)
			return false
		}

		for _, content := range res.Content {
			txt, ok := content.(*mcp.TextContent)
			require.True(t, ok)
			if txt.Text == expected {
				t.Logf("found expected notification count message: %q", expected)
				return true
			}
		}
		t.Logf("expected %q not found in tool response", expected)
		return false
	}, time.Second*3, time.Millisecond*500)
}

func stringAnyValue(s string) *v1.AnyValue {
	return &v1.AnyValue{
		Value: &v1.AnyValue_StringValue{
			StringValue: s,
		},
	}
}

func requireKeyValue(t *testing.T, attrs []*v1.KeyValue, key string, val *v1.AnyValue) {
	found := false
	for _, a := range attrs {
		if a.Key == key {
			found = a.Value.String() == val.String()
			break
		}
	}

	require.Truef(t, found, "%s=%s not found: %v", key, val.String(), attrs)
}
