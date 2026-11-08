Each provider needs an application at the provider, and each application needs the callback URL of this instance. Set the public URL first.

### Google

1. In Google Cloud, open APIs & Services → Credentials and create an OAuth client ID of type Web application.
2. Add the authorized redirect URI `https://skael.example.com/api/auth/oauth/google/callback`.
3. Paste the client ID and the client secret here.

### GitHub

1. In GitHub, open Settings → Developer settings → OAuth Apps → New OAuth App.
2. Set the homepage URL to `https://skael.example.com` and the authorization callback URL to `https://skael.example.com/api/auth/oauth/github/callback`.
3. Generate a client secret, and paste the client ID and the secret here.

This OAuth App is only for sign-in. Git sync uses the GitHub App on Settings → GitHub.

### Any OpenID Connect provider

1. In your provider (Okta, Entra ID, Auth0, Keycloak, Authentik, Google Workspace), create a web application with the authorization code flow.
2. Register the redirect URI `https://skael.example.com/api/auth/oauth/oidc/callback`.
3. Paste the issuer URL, the client ID and the client secret here. The issuer URL is the address that serves `/.well-known/openid-configuration`.

The test fetches the discovery document and checks that the issuer answers.
