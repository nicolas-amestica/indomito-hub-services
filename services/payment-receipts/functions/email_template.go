package functions

import "ind-hub-api-gox-sls-pri-gh/libs/brandedemail"

type emailDetail = brandedemail.Detail
type brandedEmail = brandedemail.Content

func renderBrandedEmail(content brandedEmail) (string, error) {
	content.AttachmentNotice = true
	return brandedemail.Render(content)
}
