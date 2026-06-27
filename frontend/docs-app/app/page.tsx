import { LandingAbout } from "@/components/LandingAbout";
import { LandingAgentsBar } from "@/components/LandingAgentsBar";
import { LandingCTA } from "@/components/LandingCTA";
import { LandingDifferentiators } from "@/components/LandingDifferentiators";
import { LandingFeatures } from "@/components/LandingFeatures";
import { LandingHero } from "@/components/LandingHero";
import { LandingHowItWorks } from "@/components/LandingHowItWorks";
import { LandingNav } from "@/components/LandingNav";
import { LandingQuickStart } from "@/components/LandingQuickStart";
import { LandingStats } from "@/components/LandingStats";
import { LandingTestimonials } from "@/components/LandingTestimonials";
import { LandingUseCases } from "@/components/LandingUseCases";
import { LandingVideo } from "@/components/LandingVideo";
import { LandingWorkflow } from "@/components/LandingWorkflow";
import { PageConstellation } from "@/components/PageConstellation";
import { ScrollRevealProvider } from "@/components/ScrollRevealProvider";
import { formatCompactNumber, getGitHubRepoStats } from "@/lib/github-repo";

export default async function HomePage() {
	const stats = await getGitHubRepoStats();
	const starsLabel = formatCompactNumber(stats.stars);

	return (
		<ScrollRevealProvider>
			<div className="landing-page relative min-h-screen overflow-hidden">
				<PageConstellation />
				<LandingNav />
				<main className="relative z-10">
					<LandingHero starsLabel={starsLabel} />
					<LandingAgentsBar />
					<LandingStats stats={stats} />
					<LandingAbout />
					<LandingVideo />
					<LandingFeatures />
					<LandingHowItWorks />
					<LandingWorkflow />
					<LandingUseCases />
					<LandingDifferentiators />
					<LandingTestimonials />
					<LandingQuickStart />
					<LandingCTA />
				</main>
			</div>
		</ScrollRevealProvider>
	);
}
