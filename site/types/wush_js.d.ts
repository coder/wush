declare global {
	function newWush(config: WushConfig): Promise<Wush>;

	interface Wush {
		auth_info(): AuthInfo;
		connect(authKey: string): Promise<Peer>;
		ping(peer: Peer): Promise<number>;
		ssh(
			peer: Peer,
			termConfig: {
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
			},
		): WushSSHSession;
		transfer(
			peer: Peer,
			filename: string,
			sizeBytes: number,
			data: ReadableStream<Uint8Array>,
			helper: (bytesRead: number) => void,
		): Promise<void>;
		stop(): void;
	}

	type AuthInfo = {
		derp_id: number;
		derp_name: string;
		auth_key: string;
	};

	type Peer = {
		id: number;
		name: string;
		ip: string;
		cancel: () => void;
	};

	interface WushSSHSession {
		resize(rows: number, cols: number): boolean;
		close(): boolean;
	}

	type WushConfig = {
		onNewPeer: (peer: Peer) => void;
		// TODO: figure out what needs to be sent to the FE
		// FE returns false if denying the file
		onIncomingFile: (
			peer: Peer,
			filename: string,
			sizeBytes: number,
		) => Promise<boolean>;
		downloadFile: (
			peer: Peer,
			filename: string,
			sizeBytes: number,
			stream: ReadableStream<Uint8Array>,
		) => Promise<void>;
	};
}

export type {};
