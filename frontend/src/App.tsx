import { QueryClientProvider } from "@tanstack/react-query";
import { ReactQueryDevtools } from "@tanstack/react-query-devtools";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { ProtectedRoute } from "./components/auth/ProtectedRoute";
import { Layout } from "./components/layout/Layout";
import { ErrorBoundary } from "./components/ui/ErrorBoundary";
import { PWAUpdatePrompt } from "./components/ui/PWAUpdatePrompt";
import { ToastContainer } from "./components/ui/ToastContainer";
import { AuthProvider } from "./contexts/AuthContext";
import { ModalProvider } from "./contexts/ModalContext";
import { ToastProvider } from "./contexts/ToastContext";
import { queryClient } from "./lib/queryClient";
import { ConfigurationPage } from "./pages/ConfigurationPage";
import { Dashboard } from "./pages/Dashboard";
import { LogsPage } from "./pages/LogsPage";
import { QueuePage } from "./pages/QueuePage";

function App() {
	return (
		<QueryClientProvider client={queryClient}>
			<ToastProvider>
				<ModalProvider>
					<AuthProvider>
						<BrowserRouter>
							<ErrorBoundary>
								<div className="min-h-screen bg-base-100">
									<Routes>
										{/* Protected routes */}
										<Route
											path="/"
											element={
												<ProtectedRoute>
													<Layout />
												</ProtectedRoute>
											}
										>
											<Route index element={<Dashboard />} />
											<Route path="queue" element={<QueuePage />} />
											<Route path="logs" element={<LogsPage />} />

											{/* Admin-only routes */}
											<Route
												path="config"
												element={
													<ProtectedRoute requireAdmin>
														<ConfigurationPage />
													</ProtectedRoute>
												}
											/>
											<Route
												path="config/:section"
												element={
													<ProtectedRoute requireAdmin>
														<ConfigurationPage />
													</ProtectedRoute>
												}
											/>
										</Route>
									</Routes>
								</div>
							</ErrorBoundary>
							<ToastContainer />
							<PWAUpdatePrompt />
						</BrowserRouter>
					</AuthProvider>
				</ModalProvider>
			</ToastProvider>
			<ReactQueryDevtools initialIsOpen={false} />
		</QueryClientProvider>
	);
}

export default App;
