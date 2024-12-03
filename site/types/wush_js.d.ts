declare global {
	function newWush(config: WushConfig): Promise<Wush>;

	interface Wush {
		auth_info(): AuthInfo;
		connect(authKey: string, offer: RTCSessionDescriptionInit): Promise<Peer>;
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

		sendWebrtcCandidate(peer: string, candidate: RTCIceCandidate);
		parseAuthKey(authKey: string): PeerAuthInfo;
	}

	type PeerAuthInfo = {
		id: string;
		type: "cli" | "web";
	};

	type AuthInfo = {
		derp_id: number;
		derp_name: string;
		derp_latency: number;
		auth_key: string;
	};

	type Peer = {
		id: string;
		name: string;
		ip: string;
		type: "cli" | "web";
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

		onWebrtcOffer: (
			id: string,
			offer: RTCSessionDescriptionInit,
		) => Promise<RTCSessionDescription | null>;
		onWebrtcAnswer: (
			id: string,
			answer: RTCSessionDescriptionInit,
		) => Promise<void>;
		onWebrtcCandidate: (
			id: string,
			candidate: RTCIceCandidateInit,
		) => Promise<void>;
	};
}

export type {};
