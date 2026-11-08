Email turns on password reset and email change. Without it, an owner resets a password with `skael-server reset-password --email <address>`.

| Provider | Host | Port | Username | Password |
|---|---|---|---|---|
| Postmark | `smtp.postmarkapp.com` | 587 | the server API token | the server API token |
| Amazon SES | `email-smtp.<region>.amazonaws.com` | 587 | the SMTP user name | the SMTP password |
| Resend | `smtp.resend.com` | 465 | `resend` | the API key |
| SendGrid | `smtp.sendgrid.net` | 587 | `apikey` | the API key |
| Mailgun | `smtp.mailgun.org` | 587 | the SMTP login | the SMTP password |
| Gmail | `smtp.gmail.com` | 587 | your address | an app password |

The provider must allow the from address. The test sends one email to your own address.
