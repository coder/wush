import type { NextApiRequest, NextApiResponse } from "next";

type RTCIceServer = {
	urls: string[];
	username?: string;
	credential?: string;
};

type IceConfig = {
	iceServers: RTCIceServer[];
};

export default function handler(
	req: NextApiRequest,
	res: NextApiResponse<IceConfig>,
) {
	if (req.method !== "GET") {
		return res.status(405).json({ error: "Method not allowed" } as any);
	}

	const iceServers: RTCIceServer[] = [
		{
			urls: ["stun:stun.l.google.com:19302"],
		},
	];

	// Add TURN server if credentials are configured
	if (
		process.env.NEXT_PUBLIC_TURN_SERVER_URL &&
		process.env.NEXT_PUBLIC_TURN_USERNAME &&
		process.env.NEXT_PUBLIC_TURN_CREDENTIAL
	) {
		iceServers.push({
			urls: [process.env.NEXT_PUBLIC_TURN_SERVER_URL],
			username: process.env.NEXT_PUBLIC_TURN_USERNAME,
			credential: process.env.NEXT_PUBLIC_TURN_CREDENTIAL,
		});
	}

	res.status(200).json({
		iceServers,
	});
}
