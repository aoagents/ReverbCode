import React from "react";
import "@/App.css";
import { BrowserRouter, Routes, Route } from "react-router-dom";
import Nav from "@/components/landing/Nav";
import Hero from "@/components/landing/Hero";
import AgentsMarquee from "@/components/landing/AgentsMarquee";
import Features from "@/components/landing/Features";
import HowItWorks from "@/components/landing/HowItWorks";
import Architecture from "@/components/landing/Architecture";
import LiveDemo from "@/components/landing/LiveDemo";
import SocialProof from "@/components/landing/SocialProof";
import CTA from "@/components/landing/CTA";
import Footer from "@/components/landing/Footer";
import VideoSection from "@/components/landing/VideoSection";
import DocsPage from "@/components/docs/DocsPage";

const Landing = () => {
  React.useEffect(() => {
    document.title = "Agent Orchestrator — Orchestration layer for parallel AI coding agents";
    const badge = document.getElementById("emergent-badge");
    if (badge) badge.remove();
  }, []);
  return (
    <div className="App relative" data-testid="landing-root">
      <Nav />
      <main className="relative z-10">
        <Hero />
        <AgentsMarquee />
        <VideoSection />
        <Features />
        <HowItWorks />
        <Architecture />
        <LiveDemo />
        <SocialProof />
        <CTA />
      </main>
      <Footer />
    </div>
  );
};

function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Landing />} />
        <Route path="/docs/*" element={<DocsPage />} />
        <Route path="*" element={<Landing />} />
      </Routes>
    </BrowserRouter>
  );
}

export default App;
