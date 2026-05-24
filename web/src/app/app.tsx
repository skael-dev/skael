import { Routes, Route } from "react-router-dom";
import { Shell } from "@/app/shell";
import { SkillList } from "@/features/skills/skill-list";
import { SkillDetail } from "@/features/skills/skill-detail";
import { Analytics } from "@/features/analytics/analytics";
import { Settings } from "@/features/settings/settings";

export function App() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route path="/" element={<SkillList />} />
        <Route path="/skills/:name" element={<SkillDetail />} />
        <Route path="/analytics" element={<Analytics />} />
        <Route path="/settings" element={<Settings />} />
      </Route>
    </Routes>
  );
}
