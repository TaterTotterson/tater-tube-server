import { AlertTriangle, RotateCcw } from "lucide-react";
import { Component, type ErrorInfo, type ReactNode } from "react";

interface ErrorBoundaryProps {
	children: ReactNode;
}

interface ErrorBoundaryState {
	error: Error | null;
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
	state: ErrorBoundaryState = { error: null };

	static getDerivedStateFromError(error: Error): ErrorBoundaryState {
		return { error };
	}

	componentDidCatch(error: Error, errorInfo: ErrorInfo) {
		console.error("Tater Tube UI crashed", error, errorInfo);
	}

	private reload = () => {
		window.location.reload();
	};

	render() {
		if (!this.state.error) {
			return this.props.children;
		}

		return (
			<div className="flex min-h-screen items-center justify-center bg-base-100 p-6">
				<div className="w-full max-w-xl rounded-lg border border-primary/30 bg-base-200/80 p-6 text-center">
					<img
						src="/tater-tube-server-mascot.png"
						alt="Tater Tube Server mascot"
						className="mx-auto mb-4 h-32 w-32 object-contain"
					/>
					<div className="mb-3 flex items-center justify-center gap-2 text-primary">
						<AlertTriangle className="h-5 w-5" />
						<h1 className="tater-glow font-vcr text-xl">Tater Tube Display Error</h1>
					</div>
					<p className="mb-4 text-base-content/70 text-sm">
						The web UI hit a display error. Reloading usually clears cached assets or a stale panel
						state.
					</p>
					<button type="button" className="btn btn-primary" onClick={this.reload}>
						<RotateCcw className="h-4 w-4" />
						Reload
					</button>
				</div>
			</div>
		);
	}
}
