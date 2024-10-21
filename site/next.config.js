// default next config async
module.exports = async () => {
  return {
    env: {
      GITHUB_STARS: await fetch("https://api.github.com/repos/coder/wush")
        .then((r) => r.json())
        .then((j) => j.stargazers_count.toString()),
    },
  };
};
