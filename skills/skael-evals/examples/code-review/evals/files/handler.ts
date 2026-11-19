import { Request, Response } from "express";
import { store } from "./store";

export async function createUser(req: Request, res: Response) {
  const { name, email } = req.body;
  let user;
  try {
    user = await store.insert(name, email);
  } catch (err: any) {
    res.status(500).send(err.message);
  }
  res.status(201).json(user);
}
