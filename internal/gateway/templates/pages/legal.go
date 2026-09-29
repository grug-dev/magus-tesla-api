package pages

import (
	"context"
	"fmt"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// legalContactEmail is the ONE address the privacy and terms pages give for data
// requests and questions. Colombian data law (Ley 1581 de 2012) expects a contact
// channel on the privacy notice. It is not translated, so it lives here and not in
// the catalogue; the sentences around it do (%s in KeyPrivacyRightsDesc and
// KeyTermsContactDesc).
//
// TODO(owner): replace the placeholder with the real address before real users arrive.
const legalContactEmail = "bytescolsas@gmail.com"

// legalPrivacySections and legalTermsSections are the two pages' content: one
// titled, described section each. Keeping them as data means adding a section is
// one line.
func legalPrivacySections(ctx context.Context) []ui.SectionHeaderProps {
	return []ui.SectionHeaderProps{
		{ID: "privacy-data", Title: i18n.T(ctx, i18n.KeyPrivacyDataTitle), Desc: i18n.T(ctx, i18n.KeyPrivacyDataDesc)},
		{ID: "privacy-use", Title: i18n.T(ctx, i18n.KeyPrivacyUseTitle), Desc: i18n.T(ctx, i18n.KeyPrivacyUseDesc)},
		{ID: "privacy-share", Title: i18n.T(ctx, i18n.KeyPrivacyShareTitle), Desc: i18n.T(ctx, i18n.KeyPrivacyShareDesc)},
		{ID: "privacy-rights", Title: i18n.T(ctx, i18n.KeyPrivacyRightsTitle), Desc: fmt.Sprintf(i18n.T(ctx, i18n.KeyPrivacyRightsDesc), legalContactEmail)},
	}
}

func legalTermsSections(ctx context.Context) []ui.SectionHeaderProps {
	return []ui.SectionHeaderProps{
		{ID: "terms-service", Title: i18n.T(ctx, i18n.KeyTermsServiceTitle), Desc: i18n.T(ctx, i18n.KeyTermsServiceDesc)},
		{ID: "terms-account", Title: i18n.T(ctx, i18n.KeyTermsAccountTitle), Desc: i18n.T(ctx, i18n.KeyTermsAccountDesc)},
		{ID: "terms-numbers", Title: i18n.T(ctx, i18n.KeyTermsNumbersTitle), Desc: i18n.T(ctx, i18n.KeyTermsNumbersDesc)},
		{ID: "terms-end", Title: i18n.T(ctx, i18n.KeyTermsEndTitle), Desc: i18n.T(ctx, i18n.KeyTermsEndDesc)},
		{ID: "terms-contact", Title: i18n.T(ctx, i18n.KeyTermsContactTitle), Desc: fmt.Sprintf(i18n.T(ctx, i18n.KeyTermsContactDesc), legalContactEmail)},
	}
}
