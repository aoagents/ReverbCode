import { LandingNav } from "../../components/LandingNav";
import { LandingHero } from "../../components/LandingHero";
import { LandingAbout } from "../../components/LandingAbout";
import { LandingAgentsBar } from "../../components/LandingAgentsBar";
import { LandingStats } from "../../components/LandingStats";
import { LandingVideo } from "../../components/LandingVideo";
import { LandingFeatures } from "../../components/LandingFeatures";
import { LandingWorkflow } from "../../components/LandingWorkflow";
import { LandingUseCases } from "../../components/LandingUseCases";
import { LandingTestimonials } from "../../components/LandingTestimonials";
import { LandingHowItWorks } from "../../components/LandingHowItWorks";
import { LandingQuickStart } from "../../components/LandingQuickStart";
import { LandingCTA } from "../../components/LandingCTA";
import { ScrollRevealProvider } from "../../components/ScrollRevealProvider";
import { PageConstellation } from "../../components/PageConstellation";
import { formatCompactNumber, getGitHubRepoStats } from "../../lib/github-repo";

export default async function LandingPage() {
  const githubStats = await getGitHubRepoStats();

  return (
    <ScrollRevealProvider>
      <PageConstellation />
      <div className="relative z-10">
        <LandingNav />
        <LandingHero starsLabel={formatCompactNumber(githubStats.stars)} />
        <LandingAbout />
        <LandingAgentsBar />
        <LandingFeatures />
        <div id="workflow"><LandingWorkflow /></div>
        <div id="usecases"><LandingUseCases /></div>
        <LandingHowItWorks />
        <LandingVideo />
        <LandingStats stats={githubStats} />
        <LandingTestimonials />
        <div id="quickstart"><LandingQuickStart /></div>
        <LandingCTA />
        <footer className="py-12 px-8 text-center text-[var(--landing-muted)] opacity-30 text-[0.8125rem] border-t border-white/[0.04]">
          MIT Licensed · Open Source
        </footer>
      </div>
    </ScrollRevealProvider>
  );
}
