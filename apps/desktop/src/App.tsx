import { Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { HomePage } from "./routes/HomePage";
import { ProjectsPage } from "./routes/ProjectsPage";
import { ProjectDetailPage } from "./routes/ProjectDetailPage";
import { InboxPage } from "./routes/InboxPage";
import { ArtifactsPage } from "./routes/ArtifactsPage";
import { SettingsPage } from "./routes/SettingsPage";

function App() {
  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<HomePage />} />
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:projectId" element={<ProjectDetailPage />} />
        <Route path="/inbox" element={<InboxPage />} />
        <Route path="/artifacts" element={<ArtifactsPage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Routes>
    </AppShell>
  );
}

export default App;
