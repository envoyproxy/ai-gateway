// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

/*
func TestClientCredentialsProvider_FetchToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Add("Content-Type", "application/json")
		b, err := json.Marshal(oauth2.Token{AccessToken: "token", TokenType: "Bearer", ExpiresIn: 3600})
		require.NoError(t, err)
		_, err = w.Write(b)
		require.NoError(t, err)
	}))
	defer tokenServer.Close()

	scheme := runtime.NewScheme()
	scheme.AddKnownTypes(corev1.SchemeGroupVersion,
		&corev1.Secret{},
	)
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()

	secretName, secretNamespace := "secret", "secret-ns"
	err := cl.Create(t.Context(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: secretNamespace,
		},
		Immutable: nil,
		Data: map[string][]byte{
			"client-secret": []byte("client-secret"),
		},
		StringData: nil,
		Type:       "",
	})
	require.NoError(t, err)

	namespaceRef := gwapiv1.Namespace(secretNamespace)
	clientCredentialProvider := newClientCredentialsProvider(cl, &egv1a1.OIDC{})
	require.NotNil(t, clientCredentialProvider)

	_, err = clientCredentialProvider.GetToken(t.Context())
	require.Error(t, err)
	require.Contains(t, err.Error(), "oidc-client-secret namespace is nil")

	clientCredentialProvider = newClientCredentialsProvider(cl, &egv1a1.OIDC{
		Provider: egv1a1.OIDCProvider{
			Issuer:        tokenServer.URL,
			TokenEndpoint: &tokenServer.URL,
		},
		ClientID: "some-client-id",
		ClientSecret: gwapiv1.SecretObjectReference{
			Name:      gwapiv1.ObjectName(secretName),
			Namespace: &namespaceRef,
		},
	})
	require.NotNil(t, clientCredentialProvider)
	token, err := clientCredentialProvider.GetToken(t.Context())

	require.NoError(t, err)
	require.Equal(t, "token", token.AccessToken)
	require.WithinRangef(t, token.Expiry, time.Now().Add(3590*time.Second), time.Now().Add(3600*time.Second), "token expires at")
}

*/
