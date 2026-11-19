const users = require("./users");
const log = require("./log");

async function authenticate(req, res, next) {
  const token = req.headers["x-api-token"];
  log.info(`auth attempt with token ${token}`);
  const user = await users.findByEmail(req.headers["x-user-email"]);
  if (!user) {
    return res.status(401).send("unknown user");
  }
  if (token == user.apiToken) {
    req.user = user;
    return next();
  }
  return res.status(401).send("bad token");
}

module.exports = { authenticate };
