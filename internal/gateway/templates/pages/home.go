package pages

import (
	"context"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// landingShot returns the /static path of one landing-page screenshot in the
// visitor's language. Each screenshot exists once per language, under
// static/img/landing/, named <name>_<lang>.png (dashboard, stats_1, stats_2,
// supercharger). To replace a screenshot, overwrite the file with the same name:
// no code changes. It is embedded in the binary, so it goes live on the next build.
func landingShot(ctx context.Context, name string) string {
	return "/static/img/landing/" + name + "_" + i18n.FromContext(ctx) + ".png"
}

// landingSampleTiles is the sample data in the hero card. The numbers are
// illustrative, not a real vehicle, and the card says so with its "Sample data"
// badge. They are plain values with units, the same in both languages, so only the
// labels go through the catalogue.
func landingSampleTiles(ctx context.Context) []ui.StatTileProps {
	return []ui.StatTileProps{
		{Label: i18n.T(ctx, i18n.KeyHomeTileBattery), Value: "67%", Desc: i18n.T(ctx, i18n.KeyHomeTileLimit)},
		{Label: i18n.T(ctx, i18n.KeyHomeTileRange), Value: "286 km"},
		{Label: i18n.T(ctx, i18n.KeyHomeTileDistance), Value: "7 km", Trend: "up-neutral", Desc: i18n.T(ctx, i18n.KeyHomeTileDistanceDelta)},
		{Label: i18n.T(ctx, i18n.KeyHomeTileEfficiency), Value: "3.5 km/%", Trend: "up", Desc: i18n.T(ctx, i18n.KeyHomeTileEfficiencyDelta)},
	}
}

// landingSteps, landingFeatures and landingDataPoints are the three lists of ui.FeatureCard
// the page renders. Keeping them as data here, not as eight copies of the same call
// in the template, means adding a feature is one line.
func landingSteps(ctx context.Context) []ui.FeatureCardProps {
	return []ui.FeatureCardProps{
		{ID: "landing-step-google", Kicker: "01", Title: i18n.T(ctx, i18n.KeyHomeStep1Title), Desc: i18n.T(ctx, i18n.KeyHomeStep1Desc)},
		{ID: "landing-step-tesla", Kicker: "02", Title: i18n.T(ctx, i18n.KeyHomeStep2Title), Desc: i18n.T(ctx, i18n.KeyHomeStep2Desc)},
		{ID: "landing-step-dashboard", Kicker: "03", Title: i18n.T(ctx, i18n.KeyHomeStep3Title), Desc: i18n.T(ctx, i18n.KeyHomeStep3Desc)},
	}
}

func landingFeatures(ctx context.Context) []ui.FeatureCardProps {
	return []ui.FeatureCardProps{
		{ID: "landing-feature-battery", Icon: "battery", Title: i18n.T(ctx, i18n.KeyHomeFeature1Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature1Desc)},
		{ID: "landing-feature-odometer", Icon: "speed", Title: i18n.T(ctx, i18n.KeyHomeFeature2Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature2Desc)},
		{ID: "landing-feature-charges", Icon: "ev_station", Title: i18n.T(ctx, i18n.KeyHomeFeature3Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature3Desc)},
		{ID: "landing-feature-efficiency", Icon: "eco", Title: i18n.T(ctx, i18n.KeyHomeFeature4Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature4Desc)},
		{ID: "landing-feature-gasoline", Icon: "local_gas_station", Title: i18n.T(ctx, i18n.KeyHomeFeature5Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature5Desc)},
		{ID: "landing-feature-battery-health", Icon: "trending_down", Title: i18n.T(ctx, i18n.KeyHomeFeature6Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature6Desc)},
		{ID: "landing-feature-tyres", Icon: "tire", Title: i18n.T(ctx, i18n.KeyHomeFeature7Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature7Desc)},
		// Community benchmark is NOT built yet — the badge says so on the page.
		{ID: "landing-feature-community", Icon: "groups", Title: i18n.T(ctx, i18n.KeyHomeFeature8Title), Desc: i18n.T(ctx, i18n.KeyHomeFeature8Desc), Badge: i18n.T(ctx, i18n.KeyHomeComingSoon)},
	}
}

func landingDataPoints(ctx context.Context) []ui.FeatureCardProps {
	return []ui.FeatureCardProps{
		{ID: "landing-data-own", Title: i18n.T(ctx, i18n.KeyHomeData1Title), Desc: i18n.T(ctx, i18n.KeyHomeData1Desc)},
		{ID: "landing-data-password", Title: i18n.T(ctx, i18n.KeyHomeData2Title), Desc: i18n.T(ctx, i18n.KeyHomeData2Desc)},
		{ID: "landing-data-tesla", Title: i18n.T(ctx, i18n.KeyHomeData3Title), Desc: i18n.T(ctx, i18n.KeyHomeData3Desc)},
	}
}

// landingStatsSlides is the stats gallery: two screenshots of /vehicle-stats. The
// first is a wide strip, so it is cropped from the top-left on a phone; the second
// is tall and always fits whole.
func landingStatsSlides(ctx context.Context) []ui.GallerySlide {
	return []ui.GallerySlide{
		{Label: i18n.T(ctx, i18n.KeyHomeStats1Title), Src: landingShot(ctx, "stats_1"), Alt: i18n.T(ctx, i18n.KeyHomeStats1Alt), Caption: i18n.T(ctx, i18n.KeyHomeStats1Caption), CropOnMobile: true},
		{Label: i18n.T(ctx, i18n.KeyHomeStats2Title), Src: landingShot(ctx, "stats_2"), Alt: i18n.T(ctx, i18n.KeyHomeStats2Alt), Caption: i18n.T(ctx, i18n.KeyHomeStats2Caption)},
	}
}
