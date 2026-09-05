import { Outlet } from "react-router-dom";
import { Navbar } from "./Navbar";
import { Sidebar } from "./Sidebar";

export function Layout() {
	return (
		<div className="drawer lg:drawer-open">
			<input id="sidebar-toggle" type="checkbox" className="drawer-toggle" />

			<div className="drawer-content min-w-0 bg-transparent">
				{/* Navbar */}
				<Navbar />

				{/* Page content */}
				<main className="min-w-0 p-4 md:p-6 xl:p-8">
					<Outlet />
				</main>
			</div>

			{/* Sidebar */}
			<div className="drawer-side">
				<label htmlFor="sidebar-toggle" aria-label="close sidebar" className="drawer-overlay" />
				<Sidebar />
			</div>
		</div>
	);
}
