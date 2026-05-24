import { Routes, Route } from "react-router-dom";
import { Shell } from "@/app/shell";
import { SkillList } from "@/features/skills/skill-list";

export function App() {
  return (
    <Routes>
      <Route element={<Shell />}>
        <Route path="/" element={<SkillList />} />
        <Route path="/skills/:name" element={<div className="p-12 text-text-primary">Detail page</div>} />
        <Route path="/analytics" element={<div className="p-12 text-text-primary">Analytics page</div>} />
        <Route path="/settings" element={<div className="p-12 text-text-primary">Settings page</div>} />
      </Route>
    </Routes>
  );
}
