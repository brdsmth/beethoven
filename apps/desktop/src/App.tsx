import { Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { MissionControlPage } from "./routes/MissionControlPage";
import { ProjectsPage } from "./routes/ProjectsPage";
import { ProjectDetailPage } from "./routes/ProjectDetailPage";
import { InboxPage } from "./routes/InboxPage";
import { AgentsPage } from "./routes/AgentsPage";
import { TimelinePage } from "./routes/TimelinePage";
import { SettingsPage } from "./routes/SettingsPage";

function App() {
  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<MissionControlPage />} />
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:projectId" element={<ProjectDetailPage />} />
        <Route path="/inbox" element={<InboxPage />} />
        <Route path="/agents" element={<AgentsPage />} />
        <Route path="/timeline" element={<TimelinePage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Routes>
    </AppShell>
  );
}

export default App;
