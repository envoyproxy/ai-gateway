// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

const patchTemplateWithCondition = `[
	{
		"op": "add",
		"path": "/status/conditions/-",
		"value": %s
	}
]`

const patchTemplateWithoutCondition = `[
	{
		"op": "add",
		"path": "/status/conditions",
		"value": [%s]
	}
]`

// patchAIGatewayRouteStatus patches status for AIGatewayRoute object.
func patchAIGatewayRouteStatus(ctx context.Context, c client.Client, route *aigv1a1.AIGatewayRoute, condition metav1.Condition) error {
	conditionJSON, err := json.Marshal(condition)
	if err != nil {
		return err
	}

	var patchString string
	if len(route.Status.Conditions) == 0 {
		patchString = fmt.Sprintf(patchTemplateWithoutCondition, string(conditionJSON))
	} else {
		patchString = fmt.Sprintf(patchTemplateWithCondition, string(conditionJSON))
	}

	return c.Patch(ctx, route, client.RawPatch(types.JSONPatchType, []byte(patchString)))
}
