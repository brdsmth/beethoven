import { Route, Routes } from "react-router-dom";
import { AppShell } from "./components/AppShell";
import { MissionControlPage } from "./routes/MissionControlPage";
import { ProjectsPage } from "./routes/ProjectsPage";
import { ProjectDetailPage } from "./routes/ProjectDetailPage";
import { TasksPage } from "./routes/TasksPage";
import { DecisionsPage } from "./routes/DecisionsPage";
import { InboxPage } from "./routes/InboxPage";
import { WorkspacesPage } from "./routes/WorkspacesPage";
import { DiffsPage } from "./routes/DiffsPage";
import { PromptsPage } from "./routes/PromptsPage";
import { RuntimePage } from "./routes/RuntimePage";
import { SettingsPage } from "./routes/SettingsPage";

function App() {
  return (
    <AppShell>
      <Routes>
        <Route path="/" element={<MissionControlPage />} />
        <Route path="/projects" element={<ProjectsPage />} />
        <Route path="/projects/:projectId" element={<ProjectDetailPage />} />
        <Route path="/tasks" element={<TasksPage />} />
        <Route path="/decisions" element={<DecisionsPage />} />
        <Route path="/inbox" element={<InboxPage />} />
        <Route path="/workspaces" element={<WorkspacesPage />} />
        <Route path="/diffs" element={<DiffsPage />} />
        <Route path="/prompts" element={<PromptsPage />} />
        <Route path="/runtime" element={<RuntimePage />} />
        <Route path="/settings" element={<SettingsPage />} />
      </Routes>
    </AppShell>
  );
}

export default App;
