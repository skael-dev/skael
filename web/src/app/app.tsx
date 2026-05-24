import { Routes, Route } from "react-router-dom";

export function App() {
  return (
    <Routes>
      <Route path="/" element={<div className="p-12 text-text-primary">Hello Skael</div>} />
      <Route path="/skills/:name" element={<div className="p-12">Skill Detail</div>} />
      <Route path="/analytics" element={<div className="p-12">Analytics</div>} />
      <Route path="/settings" element={<div className="p-12">Settings</div>} />
    </Routes>
  );
}
