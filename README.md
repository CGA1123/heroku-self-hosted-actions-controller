# heroku-self-hosted-actions-controller

Run an elastically scaled fleet of Self-Hosted GitHub Actions Runner by
listening for `workflow_job` webhook events.

You should register a new organisation webhook that listens to the
`workflow_job` event and point the webhook to `https://<app>.herokuapp.com/webhook`

Required env-vars:
- `GITHUB_TOKEN` a GitHub token with `admin:org` permissions in order to create self-hosted runner registration tokens at the org level.
- `X_HEROKU_TOKEN` a Heroku token in order to create detached run dynos running a self-hosted GitHub actions runner.
- `X_HEROKU_LOGIN` the related login for the above HEROKU_TOKEN.
- `X_HEROKU_APP` the application containing the Self-Hosted GitHub Runner code
- `GITHUB_ORG` the GitHub organisation to register runners with, should be the same one as the webhook is configured against.
- `GITHUB_SECRET` the GitHub webhook secret, used to validate incoming webhooks.
