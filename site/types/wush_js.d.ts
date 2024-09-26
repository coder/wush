declare global {
	function newWush(config: WushConfig): Promise<Wush>;
	function exitWush(): void;

	interface Wush {
		run(callbacks: WushCallbacks): void;
		ssh(termConfig: {
			writeFn: (data: string) => void;
			writeErrorFn: (err: string) => void;
			setReadFn: (readFn: (data: string) => void) => void;
			rows: number;
			cols: number;
			/** Defaults to 5 seconds */
			timeoutSeconds?: number;
			onConnectionProgress: (message: string) => void;
			onConnected: () => void;
			onDone: () => void;
		}): WushSSHSession;
	}

	interface WushSSHSession {
		resize(rows: number, cols: number): boolean;
		close(): boolean;
	}

	type WushConfig = {
		authKey?: string;
	};

	type WushCallbacks = {
		notifyState: (state: WushState) => void;
		notifyNetMap: (netMapStr: string) => void;
		notifyPanicRecover: (err: string) => void;
	};
}

export type {};
