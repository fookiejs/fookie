import { describe, it, expect } from "vitest";
import { Field, LifecycleFunction, Model, defaults } from "../../src/exports";
import { v4 } from "uuid";
import { FookieError } from "../../src/core/error";

describe("Relation", () => {
    @Model.Decorator({
        database: defaults.database.store,
        binds: {
            create: {
                role: [
                    LifecycleFunction.new({
                        key: "token_role",
                        execute: async function (payload) {
                            return payload.options.token === "token";
                        },
                    }),
                ],
            },
        },
    })
    class Token extends Model {
        @Field.Decorator({ type: defaults.type.text })
        name: string;
    }

    it("should create an entity with valid token", async () => {
        const entity = await Token.create(
            { name: v4() },
            {
                token: "token",
            },
        );

        expect(entity instanceof Token).toBe(true);
    });

    it("should fail to create an entity with invalid token", async () => {
        const entity = await Token.create(
            { name: v4() },
            {
                token: "invalid_token",
            },
        );

        expect(entity instanceof FookieError).toBe(true);
    });
});
